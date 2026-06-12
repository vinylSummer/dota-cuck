"""Worker state machine — pure transition logic, no I/O.

Kept free of protobuf and gRPC imports so it can be unit-tested in isolation.
``State`` mirrors the proto ``WorkerState`` enum names; ``grpc_client`` maps
these to the generated enum when emitting ``StatusUpdate`` events.

Transitions (per CLAUDE.md Worker State Machine):

    STOPPED --stream_connected--> IDLE
    IDLE --start_spectate--> STARTING
    STARTING --stream_started--> SPECTATING
    STARTING --stop_spectate|fatal_error--> STOPPING
    SPECTATING --stop_spectate|fatal_error--> STOPPING
    STOPPING --cleanup_done--> IDLE

The Steam Guard interrupt during STARTING does not change the worker state;
the login flow pauses in place until a code arrives, so it is not modelled as
a transition here.
"""

from __future__ import annotations

from enum import Enum, auto


class State(Enum):
    STOPPED = "STOPPED"
    STARTING = "STARTING"
    IDLE = "IDLE"
    SPECTATING = "SPECTATING"
    STOPPING = "STOPPING"


class Event(Enum):
    STREAM_CONNECTED = auto()  # gRPC stream up; worker ready to accept commands
    START_SPECTATE = auto()    # StartSpectate command received
    STREAM_STARTED = auto()    # spectate pipeline live (FFmpeg → mediamtx)
    STOP_SPECTATE = auto()     # StopSpectate command received
    FATAL_ERROR = auto()       # unrecoverable failure during STARTING/SPECTATING
    CLEANUP_DONE = auto()      # STOPPING teardown finished


class InvalidTransition(Exception):
    def __init__(self, state: State, event: Event) -> None:
        super().__init__(f"invalid transition: {event.name} in state {state.name}")
        self.state = state
        self.event = event


_TRANSITIONS: dict[tuple[State, Event], State] = {
    (State.STOPPED, Event.STREAM_CONNECTED): State.IDLE,
    (State.IDLE, Event.START_SPECTATE): State.STARTING,
    (State.STARTING, Event.STREAM_STARTED): State.SPECTATING,
    (State.STARTING, Event.STOP_SPECTATE): State.STOPPING,
    (State.STARTING, Event.FATAL_ERROR): State.STOPPING,
    (State.SPECTATING, Event.STOP_SPECTATE): State.STOPPING,
    (State.SPECTATING, Event.FATAL_ERROR): State.STOPPING,
    (State.STOPPING, Event.CLEANUP_DONE): State.IDLE,
}


def next_state(current: State, event: Event) -> State:
    """Return the state reached by applying ``event`` to ``current``.

    Raises ``InvalidTransition`` if the edge is not defined.
    """
    try:
        return _TRANSITIONS[(current, event)]
    except KeyError:
        raise InvalidTransition(current, event) from None
