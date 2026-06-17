"""Tests for watchable-match-id resolution (Task B).

The pure parsing (``extract_watchable_match_id``) and the proto mapping
(``match_id_resolved_event``) are asserted directly. ``resolve_match_id`` is
exercised against a fake python-steam client (no live Steam) with the poll sleep
zeroed; the live presence behavior is validated on-server per Known Risks.
"""

import pytest

from agent import match_id_resolved_event
from steam_client import SteamSession, extract_watchable_match_id


# --- extract_watchable_match_id (pure) --------------------------------------


@pytest.mark.parametrize(
    "rich_presence,expected",
    [
        ({"WatchableGameID": "29885123456"}, 29885123456),
        ({"WatchableGameID": 29885123456}, 29885123456),
        ({"WatchableGameID": "0"}, None),
        ({"WatchableGameID": 0}, None),
        ({"WatchableGameID": "-5"}, None),
        ({"WatchableGameID": ""}, None),
        ({"WatchableGameID": "garbage"}, None),
        ({"WatchableGameID": None}, None),
        ({}, None),
        (None, None),
    ],
)
def test_extract_watchable_match_id(rich_presence, expected):
    assert extract_watchable_match_id(rich_presence) == expected


# --- match_id_resolved_event (pure proto mapping) ---------------------------


def test_match_id_resolved_event_builds_proto():
    event = match_id_resolved_event(29885123456, "76561198000000000")
    assert event.WhichOneof("payload") == "match_id_resolved"
    assert event.match_id_resolved.match_id == 29885123456
    assert event.match_id_resolved.steam_id == "76561198000000000"


# --- resolve_match_id (fake client, no live Steam) --------------------------


class FakeUser:
    def __init__(self, rich_presence):
        self.rich_presence = rich_presence


class FakeClient:
    """Returns a scripted sequence of rich_presence dicts on get_user, exercising
    the poll loop. Already 'logged on' so resolve_match_id skips the login."""

    def __init__(self, rich_presence_sequence, steam_id=76561):
        self._seq = list(rich_presence_sequence)
        self.logged_on = True
        self.steam_id = steam_id
        self.persona_requests = []

    def set_credential_location(self, path):
        self.cred_loc = path

    def request_persona_state(self, ids, state_flags=None):
        self.persona_requests.append((list(ids), state_flags))

    def get_user(self, _steam_id):
        rp = self._seq.pop(0) if self._seq else {}
        return FakeUser(rp)


def make_session(client, monkeypatch):
    s = SteamSession(credential_location="/tmp/sentry")
    s._client = client
    s._username = "user"  # warm session: already authenticated as this user
    # Zero the poll sleep so the loop runs instantly.
    monkeypatch.setattr("steam_client.time.sleep", lambda _s: None)
    return s


def test_resolve_match_id_returns_first_present(monkeypatch):
    client = FakeClient([{"WatchableGameID": "29885123456"}])
    session = make_session(client, monkeypatch)

    assert session.resolve_match_id("76561198000000000", "user", "pass") == 29885123456
    # Requested with the RichPresence flag set.
    assert client.persona_requests[0][1] is not None
    assert client.persona_requests[0][1] & 0x200


def test_resolve_match_id_polls_until_present(monkeypatch):
    client = FakeClient(
        [{}, {"WatchableGameID": "0"}, {"WatchableGameID": "29885123456"}]
    )
    session = make_session(client, monkeypatch)

    assert session.resolve_match_id("76561198000000000", "user", "pass") == 29885123456
    assert len(client.persona_requests) == 3  # polled three times


def test_resolve_match_id_none_when_never_watchable(monkeypatch):
    client = FakeClient([{} for _ in range(50)])  # always empty
    session = make_session(client, monkeypatch)

    assert session.resolve_match_id("76561198000000000", "user", "pass") is None
