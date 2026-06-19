"""Tests for SteamSession's refresh-token acquisition and token login.

The IAuthenticationService handshake is faked by patching the module-level
``_service_post`` to return canned (real) protobuf responses, and the CM client
is faked — so these need no live Steam. The pure decision logic
(``steam_id_from_jwt``, ``classify_confirmation``) is tested directly. The live
glue (real WebAPI calls, the token CM logon, friend enumeration) is validated
on-server per Known Risks.
"""

import base64
import json
import threading
import time
import types

import pytest
from steam.enums import EResult
from steam.protobufs import steammessages_auth_pb2 as auth

import steam_client as sc
from steam_client import (
    GUARD_DEVICE_CODE,
    GUARD_DEVICE_CONFIRMATION,
    GUARD_EMAIL_CODE,
    GUARD_NONE,
    LoginError,
    SteamGuardRequired,
    SteamSession,
    classify_confirmation,
    steam_id_from_jwt,
)


def jwt_for(steamid: str) -> str:
    """Build a minimal JWT whose payload carries ``sub`` = steamid."""
    payload = base64.urlsafe_b64encode(json.dumps({"sub": steamid}).encode())
    return f"hdr.{payload.rstrip(b'=').decode()}.sig"


# --- steam_id_from_jwt (pure) ----------------------------------------------


def test_steam_id_from_jwt_reads_sub():
    assert steam_id_from_jwt(jwt_for("76561198179568701")) == "76561198179568701"


@pytest.mark.parametrize("token", ["", "garbage", "only.two", "a.!!!.c"])
def test_steam_id_from_jwt_malformed_returns_empty(token):
    assert steam_id_from_jwt(token) == ""


# --- classify_confirmation (pure) ------------------------------------------


@pytest.mark.parametrize(
    "types,expected",
    [
        ([GUARD_EMAIL_CODE], "email"),
        ([GUARD_DEVICE_CODE], "device"),
        ([GUARD_EMAIL_CODE, GUARD_DEVICE_CONFIRMATION], "email"),  # code wins
        ([GUARD_DEVICE_CONFIRMATION], "poll"),
        ([GUARD_NONE], "none"),
        ([], "none"),
    ],
)
def test_classify_confirmation(types, expected):
    assert classify_confirmation(types) == expected


# --- fake auth API ----------------------------------------------------------


class FakeAuthAPI:
    """Stand-in for ``_service_post``: scripts the IAuthenticationService
    handshake. Records every request, returns a token after ``polls_to_token``
    poll calls, and optionally rotates the QR challenge URL on the first poll."""

    def __init__(
        self, refresh_token, *, confirmations=(), polls_to_token=1, rotate=False
    ):
        self.refresh_token = refresh_token
        self.confirmations = confirmations
        self.polls_to_token = polls_to_token
        self.rotate = rotate
        self.requests = []
        self._polls = 0

    def __call__(self, method, request, response_cls):
        self.requests.append((method, request))
        resp = response_cls()
        if method in ("BeginAuthSessionViaQR", "BeginAuthSessionViaCredentials"):
            resp.client_id = 100
            resp.request_id = b"reqid"
            resp.interval = 0
            if method == "BeginAuthSessionViaQR":
                resp.challenge_url = "https://s.team/q/INITIAL"
            else:
                resp.steamid = 76561198000000000
            for ct in self.confirmations:
                resp.allowed_confirmations.add().confirmation_type = ct
        elif method == "GetPasswordRSAPublicKey":
            resp.publickey_mod = "AB"
            resp.publickey_exp = "010001"
            resp.timestamp = 123
        elif method == "PollAuthSessionStatus":
            self._polls += 1
            if self.rotate and self._polls == 1:
                resp.new_challenge_url = "https://s.team/q/ROTATED"
            if self._polls >= self.polls_to_token:
                resp.refresh_token = self.refresh_token
        elif method == "UpdateAuthSessionWithSteamGuardCode":
            pass
        return resp

    def method_names(self):
        return [m for m, _ in self.requests]


@pytest.fixture
def patch_auth(monkeypatch):
    def install(api):
        monkeypatch.setattr(sc, "_service_post", api)
        # Skip real RSA so the credentials path needs no valid key material.
        monkeypatch.setattr(sc, "_rsa_encrypt_password", lambda pw, mod, exp: "ENC")
        return api

    return install


# --- QR link ----------------------------------------------------------------


def test_begin_qr_link_emits_challenge_and_returns_token(patch_auth):
    token = jwt_for("76561198000000001")
    patch_auth(FakeAuthAPI(token, rotate=True, polls_to_token=2))
    session = SteamSession()

    urls = []
    owner, refresh = session.begin_qr_link(on_challenge=urls.append)

    assert owner == "76561198000000001"
    assert refresh == token
    # Initial URL, then the rotated one surfaced from the poll.
    assert urls == ["https://s.team/q/INITIAL", "https://s.team/q/ROTATED"]


# --- credentials link -------------------------------------------------------


def test_begin_credentials_link_no_guard(patch_auth):
    token = jwt_for("76561198000000002")
    api = patch_auth(FakeAuthAPI(token, confirmations=[GUARD_NONE]))
    session = SteamSession()

    owner, refresh = session.begin_credentials_link("user", "pass", on_guard=None)

    assert owner == "76561198000000002"
    assert refresh == token
    assert "UpdateAuthSessionWithSteamGuardCode" not in api.method_names()


def test_begin_credentials_link_email_guard_handoff(patch_auth):
    token = jwt_for("42")
    api = patch_auth(FakeAuthAPI(token, confirmations=[GUARD_EMAIL_CODE]))
    session = SteamSession()

    result = {}
    guards = []

    def target():
        try:
            result["v"] = session.begin_credentials_link(
                "user", "pass", on_guard=guards.append
            )
        except Exception as exc:  # noqa: BLE001
            result["exc"] = exc

    t = threading.Thread(target=target)
    t.start()
    _wait_until(lambda: guards == ["EMAIL"])
    session.submit_guard_code("WXYZ")
    t.join(timeout=2)

    assert result["v"] == ("42", token)
    # The submitted code was sent in the Update request with the EmailCode type.
    update = next(
        r for m, r in api.requests if m == "UpdateAuthSessionWithSteamGuardCode"
    )
    assert update.code == "WXYZ"
    assert update.code_type == auth.k_EAuthSessionGuardType_EmailCode


def test_begin_credentials_link_device_guard_uses_mobile(patch_auth):
    token = jwt_for("7")
    api = patch_auth(FakeAuthAPI(token, confirmations=[GUARD_DEVICE_CODE]))
    session = SteamSession()

    guards = []
    t = threading.Thread(
        target=lambda: session.begin_credentials_link("u", "p", on_guard=guards.append)
    )
    t.start()
    _wait_until(lambda: guards == ["MOBILE"])
    session.submit_guard_code("123456")
    t.join(timeout=2)

    update = next(
        r for m, r in api.requests if m == "UpdateAuthSessionWithSteamGuardCode"
    )
    assert update.code_type == auth.k_EAuthSessionGuardType_DeviceCode


def test_begin_credentials_link_guard_without_callback_raises(patch_auth):
    patch_auth(FakeAuthAPI(jwt_for("1"), confirmations=[GUARD_EMAIL_CODE]))
    session = SteamSession()

    with pytest.raises(SteamGuardRequired) as ei:
        session.begin_credentials_link("u", "p", on_guard=None)
    assert ei.value.guard_type == "EMAIL"


def test_begin_credentials_link_invalid_credentials_raises(patch_auth):
    class RejectAPI(FakeAuthAPI):
        def __call__(self, method, request, response_cls):
            self.requests.append((method, request))
            resp = response_cls()
            if method == "GetPasswordRSAPublicKey":
                resp.publickey_mod, resp.publickey_exp, resp.timestamp = (
                    "AB",
                    "010001",
                    1,
                )
            # BeginAuthSessionViaCredentials returns an empty (no client_id) response.
            return resp

    patch_auth(RejectAPI(jwt_for("1")))
    session = SteamSession()
    with pytest.raises(LoginError):
        session.begin_credentials_link("u", "badpass", on_guard=None)


# --- token CM login + friends -----------------------------------------------


class FakeMsgResp:
    def __init__(self, eresult):
        self.body = types.SimpleNamespace(eresult=eresult)


class FakeFriend:
    def __init__(self, sid, name, state, app_id=None):
        self.steam_id = types.SimpleNamespace(as_64=sid)
        self.name = name
        self.state = state
        self._app = app_id

    def get_ps(self, key):
        return self._app if key == "game_played_app_id" else None


class FakeCMClient:
    """Minimal CM client supporting login_with_token + friend enumeration."""

    def __init__(self, eresult=EResult.OK, friends=()):
        self.chat_mode = 2
        self.connection = types.SimpleNamespace(local_address="127.0.0.1")
        self.logged_on = False
        self.friends = friends
        self._eresult = eresult
        self.sent = []

    def _pre_login(self):
        return EResult.OK

    def send(self, message):
        self.sent.append(message)

    def wait_msg(self, emsg, timeout=None):
        if self._eresult == EResult.OK:
            self.logged_on = True
        return FakeMsgResp(self._eresult)

    def sleep(self, _seconds):
        pass


def make_session(client):
    s = SteamSession()
    s._client = client
    return s


def test_login_with_token_sends_access_token_and_returns_owner():
    token = jwt_for("76561198000000009")
    client = FakeCMClient()
    session = make_session(client)

    owner = session.login_with_token(token)

    assert owner == "76561198000000009"
    [message] = client.sent
    assert message.body.access_token == token
    assert not message.body.password  # no password on a token logon


def test_login_with_token_idempotent_when_already_logged_in():
    token = jwt_for("55")
    client = FakeCMClient()
    session = make_session(client)
    session.login_with_token(token)

    session.login_with_token(token)  # same account, already logged on

    assert len(client.sent) == 1  # no second logon


def test_login_with_token_failure_raises():
    client = FakeCMClient(eresult=EResult.AccessDenied)
    session = make_session(client)
    with pytest.raises(LoginError):
        session.login_with_token(jwt_for("1"))


def test_list_friends_maps_status():
    client = FakeCMClient(
        friends=[
            FakeFriend(10, "ana", state=1, app_id=sc.DOTA2_APP_ID),
            FakeFriend(20, "bob", state=0, app_id=None),
        ]
    )
    session = make_session(client)

    owner, friends = session.list_friends(jwt_for("99"))

    assert owner == "99"
    assert friends[0] == {
        "steam_id": "10",
        "persona_name": "ana",
        "online": True,
        "in_match": True,
    }
    assert friends[1]["online"] is False


def _wait_until(pred, timeout=2.0):
    start = time.monotonic()
    while time.monotonic() - start < timeout:
        if pred():
            return
        time.sleep(0.005)
    raise AssertionError("condition not met within timeout")
