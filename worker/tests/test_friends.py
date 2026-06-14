"""Tests for the in-process friends path: status derivation, the FriendsResult
proto mapping, and the agent's ListFriends handler routing. The warm Steam
session is faked, so these need no live Steam (the python-steam glue in
steam_client.SteamSession is validated on-server)."""

import pytest

from agent import Agent, friends_error_event, friends_ok_event
from spectator.v1 import worker_pb2 as pb
from steam_client import DOTA2_APP_ID, LoginError, SteamGuardRequired, derive_status


# --- derive_status ----------------------------------------------------------


@pytest.mark.parametrize(
    "persona_state,game_app_id,expected",
    [
        (0, None, (False, False)),  # offline, no game
        (0, DOTA2_APP_ID, (False, True)),  # in a Dota match while "offline"
        (1, None, (True, False)),  # online, no game
        (1, DOTA2_APP_ID, (True, True)),  # online, in a Dota match
        (3, 730, (True, False)),  # online, in a different game
        (1, 0, (True, False)),  # online, app id 0 is no game
    ],
)
def test_derive_status(persona_state, game_app_id, expected):
    assert derive_status(persona_state, game_app_id) == expected


# --- proto mapping ----------------------------------------------------------


def test_friends_ok_event():
    event = friends_ok_event(
        "req-7",
        "owner1",
        [
            {"steam_id": "10", "persona_name": "x", "online": True, "in_match": True},
            {"steam_id": "20", "persona_name": "y", "online": False, "in_match": False},
        ],
    )

    res = event.friends_result
    assert res.request_id == "req-7"
    assert res.owner_steam_id == "owner1"
    assert not res.HasField("error")
    assert [f.steam_id for f in res.friends] == ["10", "20"]
    assert res.friends[0].online is True
    assert res.friends[0].in_match is True
    assert res.friends[1].online is False


@pytest.mark.parametrize(
    "exc,code",
    [
        (SteamGuardRequired("MOBILE"), "STEAM_GUARD_REQUIRED"),
        (LoginError("bad password"), "LOGIN_FAILED"),
        (RuntimeError("socket closed"), "STEAM_ERROR"),
    ],
)
def test_friends_error_event(exc, code):
    event = friends_error_event("req-8", exc)

    res = event.friends_result
    assert res.request_id == "req-8"
    assert len(res.friends) == 0
    assert res.error.code == code
    assert res.error.message == str(exc)
    assert res.error.fatal is False


# --- agent ListFriends handler ----------------------------------------------


class FakeSession:
    def __init__(self, result=None, exc=None):
        self.result = result if result is not None else ("owner", [])
        self.exc = exc
        self.calls = []

    def list_friends(self, username, password, sentry):
        self.calls.append((username, password, sentry))
        if self.exc:
            raise self.exc
        return self.result


class CapturingClient:
    def __init__(self):
        self.sent = []

    def send(self, event):
        self.sent.append(event)


def make_agent(session):
    agent = Agent("addr:0", "worker-1", steam_session=session)
    agent._client = CapturingClient()
    return agent


def test_list_friends_success_sends_friends_result():
    session = FakeSession(
        result=("owner99", [{"steam_id": "1", "persona_name": "a", "online": True}])
    )
    agent = make_agent(session)

    agent._list_friends(
        pb.ListFriends(request_id="r", steam_username="u", steam_password="p")
    )

    assert session.calls == [("u", "p", None)]
    [event] = agent._client.sent
    res = event.friends_result
    assert res.request_id == "r"
    assert res.owner_steam_id == "owner99"
    assert res.friends[0].steam_id == "1"


def test_list_friends_passes_sentry():
    session = FakeSession()
    agent = make_agent(session)

    agent._list_friends(
        pb.ListFriends(
            request_id="r",
            steam_username="u",
            steam_password="p",
            sentry_hash=b"\x01\x02",
        )
    )

    assert session.calls[0][2] == "\x01\x02"


@pytest.mark.parametrize(
    "exc,code",
    [
        (SteamGuardRequired("EMAIL"), "STEAM_GUARD_REQUIRED"),
        (LoginError("nope"), "LOGIN_FAILED"),
        (RuntimeError("boom"), "STEAM_ERROR"),
    ],
)
def test_list_friends_failure_sends_error(exc, code):
    agent = make_agent(FakeSession(exc=exc))

    agent._list_friends(
        pb.ListFriends(request_id="r", steam_username="u", steam_password="p")
    )

    [event] = agent._client.sent
    assert event.friends_result.error.code == code
