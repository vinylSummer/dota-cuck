"""Tests for the LinkAccount path: the LinkResult / SteamGuardRequired proto
mapping and the agent's LinkAccount handler routing. The Steam session is faked,
so these need no live Steam (the python-steam glue is validated on-server)."""

import pytest

from agent import (
    Agent,
    link_error_event,
    link_ok_event,
    steam_guard_event,
)
from spectator.v1 import worker_pb2 as pb
from steam_client import LoginError


# --- proto mapping ----------------------------------------------------------


def test_link_ok_event():
    event = link_ok_event("req-1", "76561198000000000")
    res = event.link_result
    assert res.request_id == "req-1"
    assert res.owner_steam_id == "76561198000000000"
    assert not res.HasField("error")


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
    """Records the link() call and optionally fires on_guard / raises."""

    def __init__(self, owner="owner", exc=None, guard_type=None):
        self.owner = owner
        self.exc = exc
        self.guard_type = guard_type
        self.calls = []

    def link(self, username, password, on_guard=None):
        self.calls.append((username, password))
        if self.guard_type and on_guard is not None:
            on_guard(self.guard_type)
        if self.exc:
            raise self.exc
        return self.owner

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


def test_link_account_success_sends_link_result():
    session = FakeSession(owner="76561198999")
    agent = make_agent(session)

    agent._link_account(
        pb.LinkAccount(request_id="r", steam_username="u", steam_password="p")
    )

    assert session.calls == [("u", "p")]
    [event] = agent._client.sent
    assert event.link_result.request_id == "r"
    assert event.link_result.owner_steam_id == "76561198999"


def test_link_account_guard_then_success_sends_two_events():
    session = FakeSession(owner="owner1", guard_type="MOBILE")
    agent = make_agent(session)

    agent._link_account(
        pb.LinkAccount(request_id="r", steam_username="u", steam_password="p")
    )

    guard_ev, result_ev = agent._client.sent
    assert guard_ev.steam_guard.request_id == "r"
    assert guard_ev.steam_guard.guard_type == pb.MOBILE
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

    agent._link_account(
        pb.LinkAccount(request_id="r", steam_username="u", steam_password="p")
    )

    [event] = agent._client.sent
    assert event.link_result.error.code == code


def test_on_steam_guard_submits_code_to_session():
    session = FakeSession()
    agent = make_agent(session)

    agent._on_steam_guard(pb.SubmitSteamGuardCode(code="ABCDE", request_id="r"))

    assert session.submitted == "ABCDE"
