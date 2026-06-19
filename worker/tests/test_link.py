"""Tests for the LinkAccount path: the LinkResult / SteamQrChallenge /
SteamGuardRequired proto mapping and the agent's LinkAccount handler routing
(QR vs credentials). The Steam session is faked, so these need no live Steam
(the python-steam glue is validated on-server)."""

import pytest

from agent import (
    Agent,
    link_error_event,
    link_ok_event,
    qr_challenge_event,
    steam_guard_event,
)
from spectator.v1 import worker_pb2 as pb
from steam_client import LoginError


# --- proto mapping ----------------------------------------------------------


def test_link_ok_event_carries_refresh_token():
    event = link_ok_event("req-1", "76561198000000000", "refresh-xyz")
    res = event.link_result
    assert res.request_id == "req-1"
    assert res.owner_steam_id == "76561198000000000"
    assert res.refresh_token == "refresh-xyz"
    assert not res.HasField("error")


def test_qr_challenge_event():
    ev = qr_challenge_event("req-9", "https://s.team/q/ABC").qr_challenge
    assert ev.request_id == "req-9"
    assert ev.challenge_url == "https://s.team/q/ABC"


@pytest.mark.parametrize(
    "exc,code",
    [
        (LoginError("bad password"), "LOGIN_FAILED"),
        (RuntimeError("socket closed"), "STEAM_ERROR"),
    ],
)
def test_link_error_event(exc, code):
    res = link_error_event("req-2", exc).link_result
    assert res.request_id == "req-2"
    assert res.owner_steam_id == ""
    assert res.refresh_token == ""
    assert res.error.code == code
    assert res.error.message == str(exc)
    assert res.error.fatal is False


@pytest.mark.parametrize(
    "guard_type,expected",
    [
        ("EMAIL", pb.EMAIL),
        ("MOBILE", pb.MOBILE),
        ("???", pb.STEAM_GUARD_TYPE_UNSPECIFIED),
    ],
)
def test_steam_guard_event(guard_type, expected):
    ev = steam_guard_event("req-3", guard_type).steam_guard
    assert ev.request_id == "req-3"
    assert ev.guard_type == expected


# --- agent LinkAccount handler ----------------------------------------------


class FakeSession:
    """Records which acquisition path ran and optionally fires callbacks / raises."""

    def __init__(
        self, owner="owner", token="tok", exc=None, guard_type=None, challenge=None
    ):
        self.owner = owner
        self.token = token
        self.exc = exc
        self.guard_type = guard_type
        self.challenge = challenge
        self.qr_calls = 0
        self.cred_calls = []

    def begin_qr_link(self, on_challenge=None):
        self.qr_calls += 1
        if self.challenge and on_challenge is not None:
            on_challenge(self.challenge)
        if self.exc:
            raise self.exc
        return self.owner, self.token

    def begin_credentials_link(self, username, password, on_guard=None):
        self.cred_calls.append((username, password))
        if self.guard_type and on_guard is not None:
            on_guard(self.guard_type)
        if self.exc:
            raise self.exc
        return self.owner, self.token

    def submit_guard_code(self, code):
        self.submitted = code


class CapturingClient:
    def __init__(self):
        self.sent = []

    def send(self, event):
        self.sent.append(event)


def make_agent(session):
    agent = Agent("addr:0", "worker-1", steam_session=session)
    agent._client = CapturingClient()
    return agent


def test_link_account_qr_mode_when_no_credentials():
    session = FakeSession(
        owner="76561198999", token="rt", challenge="https://s.team/q/Z"
    )
    agent = make_agent(session)

    agent._link_account(pb.LinkAccount(request_id="r"))

    assert session.qr_calls == 1
    assert session.cred_calls == []
    challenge_ev, result_ev = agent._client.sent
    assert challenge_ev.qr_challenge.challenge_url == "https://s.team/q/Z"
    assert result_ev.link_result.owner_steam_id == "76561198999"
    assert result_ev.link_result.refresh_token == "rt"


def test_link_account_credentials_mode_when_creds_present():
    session = FakeSession(owner="o", token="rt")
    agent = make_agent(session)

    agent._link_account(
        pb.LinkAccount(request_id="r", steam_username="u", steam_password="p")
    )

    assert session.qr_calls == 0
    assert session.cred_calls == [("u", "p")]
    [event] = agent._client.sent
    assert event.link_result.refresh_token == "rt"


def test_link_account_credentials_guard_then_success_sends_two_events():
    session = FakeSession(owner="owner1", guard_type="EMAIL")
    agent = make_agent(session)

    agent._link_account(
        pb.LinkAccount(request_id="r", steam_username="u", steam_password="p")
    )

    guard_ev, result_ev = agent._client.sent
    assert guard_ev.steam_guard.request_id == "r"
    assert guard_ev.steam_guard.guard_type == pb.EMAIL
    assert result_ev.link_result.owner_steam_id == "owner1"


@pytest.mark.parametrize(
    "exc,code",
    [
        (LoginError("nope"), "LOGIN_FAILED"),
        (RuntimeError("boom"), "STEAM_ERROR"),
    ],
)
def test_link_account_failure_sends_error(exc, code):
    agent = make_agent(FakeSession(exc=exc))

    agent._link_account(pb.LinkAccount(request_id="r"))

    [event] = agent._client.sent
    assert event.link_result.error.code == code


def test_on_steam_guard_submits_code_to_session():
    session = FakeSession()
    agent = make_agent(session)

    agent._on_steam_guard(pb.SubmitSteamGuardCode(code="ABCDE", request_id="r"))

    assert session.submitted == "ABCDE"
