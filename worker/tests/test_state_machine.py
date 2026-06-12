import pytest

import state_machine as sm
from state_machine import Event, InvalidTransition, State, next_state

# Every defined edge: (from, event, to).
VALID = [
    (State.STOPPED, Event.STREAM_CONNECTED, State.IDLE),
    (State.IDLE, Event.START_SPECTATE, State.STARTING),
    (State.STARTING, Event.STREAM_STARTED, State.SPECTATING),
    (State.STARTING, Event.STOP_SPECTATE, State.STOPPING),
    (State.STARTING, Event.FATAL_ERROR, State.STOPPING),
    (State.SPECTATING, Event.STOP_SPECTATE, State.STOPPING),
    (State.SPECTATING, Event.FATAL_ERROR, State.STOPPING),
    (State.STOPPING, Event.CLEANUP_DONE, State.IDLE),
]


@pytest.mark.parametrize("current,event,expected", VALID)
def test_valid_transitions(current, event, expected):
    assert next_state(current, event) == expected


# Any (state, event) pair not in VALID must be rejected.
_VALID_PAIRS = {(c, e) for c, e, _ in VALID}
INVALID = [
    (c, e) for c in State for e in Event if (c, e) not in _VALID_PAIRS
]


@pytest.mark.parametrize("current,event", INVALID)
def test_invalid_transitions_raise(current, event):
    with pytest.raises(InvalidTransition) as exc:
        next_state(current, event)
    assert exc.value.state == current
    assert exc.value.event == event


def test_happy_path_round_trip():
    state = State.STOPPED
    for event in (
        Event.STREAM_CONNECTED,
        Event.START_SPECTATE,
        Event.STREAM_STARTED,
        Event.STOP_SPECTATE,
        Event.CLEANUP_DONE,
    ):
        state = next_state(state, event)
    assert state == State.IDLE


def test_fatal_error_from_spectating_routes_to_stopping():
    assert next_state(State.SPECTATING, Event.FATAL_ERROR) == State.STOPPING
