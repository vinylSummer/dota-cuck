"""Tests for the SteamSession interactive login loop and Steam Guard handoff.

These drive a fake python-steam client (so no live Steam), but use the real
EResult enum — it ships with the worker's `steam` dependency. The live glue
(actual login/sentry/friend enumeration) is validated on-server per Known Risks.
"""

import threading

import pytest
from steam.enums import EResult

from steam_client import (
    LoginError,
    SteamGuardRequired,
    SteamSession,
    _classify_login,
)


# --- _classify_login (pure) -------------------------------------------------


@pytest.mark.parametrize(
    "result,expected",
    [
        (EResult.OK, ("ok", None)),
        (EResult.AccountLoginDeniedNeedTwoFactor, ("guard", "MOBILE")),
        (EResult.TwoFactorCodeMismatch, ("guard", "MOBILE")),
        (EResult.AccountLogonDenied, ("guard", "EMAIL")),
        (EResult.InvalidPassword, ("fail", None)),
    ],
)
def test_classify_login(result, expected):
    assert _classify_login(result) == expected


# --- fake client ------------------------------------------------------------


class FakeSteamID:
    def __init__(self, value):
        self.as_64 = value


class FakeClient:
    """Records every login() call and returns a scripted sequence of EResults."""

    def __init__(self, results, steam_id=76561):
        self._results = list(results)
        self.calls = []
        self.logged_on = False
        self.steam_id = FakeSteamID(steam_id)

    def set_credential_location(self, path):
        self.cred_loc = path

    def login(self, username, password, **kwargs):
        self.calls.append({"username": username, "password": password, **kwargs})
        result = self._results.pop(0)
        if result == EResult.OK:
            self.logged_on = True
        return result


def make_session(client):
    s = SteamSession(credential_location="/tmp/sentry")
    s._client = client
    return s


# --- link: happy path -------------------------------------------------------


def test_link_no_guard_returns_owner_steam_id():
    client = FakeClient([EResult.OK], steam_id=99)
    session = make_session(client)

    owner = session.link("user", "pass")

    assert owner == "99"
    assert client.calls == [{"username": "user", "password": "pass"}]


def test_link_warm_session_skips_relogin():
    client = FakeClient([EResult.OK], steam_id=7)
    session = make_session(client)
    session.link("user", "pass")

    session.link("user", "pass")  # already logged on as this user

    assert len(client.calls) == 1  # no second login


# --- link: interactive Steam Guard ------------------------------------------


def _run_link_in_thread(session):
    result = {}

    def target():
        try:
            result["owner"] = session.link("user", "pass", on_guard=guards.append)
        except Exception as exc:  # noqa: BLE001
            result["exc"] = exc

    guards: list[str] = []
    t = threading.Thread(target=target)
    t.start()
    return t, result, guards


def test_link_mobile_guard_resumes_with_two_factor_code():
    client = FakeClient([EResult.AccountLoginDeniedNeedTwoFactor, EResult.OK], steam_id=42)
    session = make_session(client)

    t, result, guards = _run_link_in_thread(session)
    # Wait until the login has paused on the guard prompt, then deliver the code.
    _wait_until(lambda: guards == ["MOBILE"])
    session.submit_guard_code("123456")
    t.join(timeout=2)

    assert result["owner"] == "42"
    assert client.calls[1]["two_factor_code"] == "123456"
    assert "auth_code" not in client.calls[1]


def test_link_email_guard_resumes_with_auth_code():
    client = FakeClient([EResult.AccountLogonDenied, EResult.OK])
    session = make_session(client)

    t, result, guards = _run_link_in_thread(session)
    _wait_until(lambda: guards == ["EMAIL"])
    session.submit_guard_code("WXYZ")
    t.join(timeout=2)

    assert client.calls[1]["auth_code"] == "WXYZ"
    assert "exc" not in result


def test_link_wrong_code_reprompts_then_succeeds():
    client = FakeClient(
        [EResult.AccountLoginDeniedNeedTwoFactor, EResult.TwoFactorCodeMismatch, EResult.OK]
    )
    session = make_session(client)

    t, result, guards = _run_link_in_thread(session)
    _wait_until(lambda: len(guards) == 1)
    session.submit_guard_code("bad")
    _wait_until(lambda: len(guards) == 2)
    session.submit_guard_code("good")
    t.join(timeout=2)

    assert guards == ["MOBILE", "MOBILE"]
    assert client.calls[1]["two_factor_code"] == "bad"
    assert client.calls[2]["two_factor_code"] == "good"


# --- failures ---------------------------------------------------------------


def test_link_fatal_result_raises_login_error():
    client = FakeClient([EResult.InvalidPassword])
    session = make_session(client)

    with pytest.raises(LoginError):
        session.link("user", "pass")


def test_list_friends_raises_on_guard_without_callback():
    client = FakeClient([EResult.AccountLogonDenied])
    session = make_session(client)

    with pytest.raises(SteamGuardRequired) as ei:
        session.list_friends("user", "pass")
    assert ei.value.guard_type == "EMAIL"


def _wait_until(pred, timeout=2.0):
    import time

    start = time.monotonic()
    while time.monotonic() - start < timeout:
        if pred():
            return
        time.sleep(0.005)
    raise AssertionError("condition not met within timeout")
