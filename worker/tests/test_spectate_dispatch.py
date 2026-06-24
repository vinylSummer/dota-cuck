"""Tests for the agent's StartSpectate dispatch: match-id resolution outcomes and the handoff to
the Dota GUI automation. These are decisions (which event/error is emitted, whether spectate runs),
not the uinput/OCR glue — that is validated live in the harness.

``_start_spectate`` is driven directly (the synchronous worker body, not the threaded entry) against
a fake steam session + fake DotaClient + a capturing gRPC client; the worker starts in IDLE so the
IDLE -> STARTING transition is valid.
"""

import os
import sys

import pytest

import state_machine as sm
from agent import Agent
from dota_client import SpectateError

_GEN = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "gen")
if _GEN not in sys.path:
    sys.path.insert(0, _GEN)

from spectator.v1 import worker_pb2 as pb  # noqa: E402


class FakeSession:
    def __init__(self, match_id=None, persona="zitraks mops", resolve_exc=None):
        self._match_id = match_id
        self._persona = persona
        self._resolve_exc = resolve_exc
        self.resolve_calls = []

    def resolve_match_id(self, target_steam_id, refresh_token):
        self.resolve_calls.append((target_steam_id, refresh_token))
        if self._resolve_exc:
            raise self._resolve_exc
        return self._match_id

    def persona_name(self, target_steam_id):
        return self._persona


class FakeDota:
    def __init__(self, exc=None):
        self.exc = exc
        self.spectated = []

    def spectate(self, target_name):
        self.spectated.append(target_name)
        if self.exc:
            raise self.exc


class CapturingClient:
    def __init__(self):
        self.sent = []

    def send(self, event):
        self.sent.append(event)


def make_agent(session, dota=None):
    agent = Agent("addr:0", "worker-1", steam_session=session, dota_client=dota)
    agent._client = CapturingClient()
    agent.state = sm.State.IDLE  # IDLE -> STARTING is the valid StartSpectate transition
    return agent


def _events(agent, which):
    return [e for e in agent._client.sent if e.WhichOneof("payload") == which]


def _start(agent, target="76561198000000000"):
    agent._start_spectate(pb.StartSpectate(session_id="s1", target_steam_id=target, refresh_token="rt"))


def test_no_watchable_match_fails_without_spectating():
    session = FakeSession(match_id=None)
    dota = FakeDota()
    agent = make_agent(session, dota)

    _start(agent)

    assert dota.spectated == []
    [err] = _events(agent, "error")
    assert err.error.code == "NO_WATCHABLE_MATCH"
    assert err.error.fatal
    assert _events(agent, "match_id_resolved") == []


def test_resolve_failure_is_fatal():
    session = FakeSession(resolve_exc=RuntimeError("steam down"))
    agent = make_agent(session, FakeDota())

    _start(agent)

    [err] = _events(agent, "error")
    assert err.error.code == "MATCH_RESOLVE_FAILED"


def test_no_dota_client_resolves_then_stops_at_handoff():
    session = FakeSession(match_id=29885123456)
    agent = make_agent(session, dota=None)

    _start(agent)

    [resolved] = _events(agent, "match_id_resolved")
    assert resolved.match_id_resolved.match_id == 29885123456
    assert _events(agent, "error") == []


def test_match_resolved_drives_spectate_with_persona_name():
    session = FakeSession(match_id=29885123456, persona="zitraks mops")
    dota = FakeDota()
    agent = make_agent(session, dota)

    _start(agent)

    assert dota.spectated == ["zitraks mops"]
    [resolved] = _events(agent, "match_id_resolved")
    assert resolved.match_id_resolved.match_id == 29885123456
    assert _events(agent, "error") == []


@pytest.mark.parametrize("code", ["FRIEND_NOT_FOUND", "SPECTATE_FAILED"])
def test_spectate_error_maps_to_fatal_error_event(code):
    session = FakeSession(match_id=29885123456)
    dota = FakeDota(exc=SpectateError(code, "boom"))
    agent = make_agent(session, dota)

    _start(agent)

    # match_id resolved first, then the GUI spectate fails fatally.
    assert _events(agent, "match_id_resolved")
    [err] = _events(agent, "error")
    assert err.error.code == code
    assert err.error.fatal


def test_unexpected_spectate_exception_is_fatal_spectate_failed():
    session = FakeSession(match_id=29885123456)
    dota = FakeDota(exc=RuntimeError("device gone"))
    agent = make_agent(session, dota)

    _start(agent)

    [err] = _events(agent, "error")
    assert err.error.code == "SPECTATE_FAILED"
