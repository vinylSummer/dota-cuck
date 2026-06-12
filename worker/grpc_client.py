"""gRPC bidirectional stream client and command dispatch.

The worker is the gRPC *client*. On startup it opens a single long-lived
``ControlPlaneService.WorkerSession`` stream, pushes ``WorkerEvent`` messages
up, and receives ``Command`` messages down.

``CommandDispatcher`` (pure routing, no network) is split out so it can be
unit-tested by feeding it ``Command`` messages and asserting the right handler
fires. ``GrpcClient`` is the network glue around it.
"""

from __future__ import annotations

import logging
import os
import queue
import sys
from typing import Callable, Iterator

# Generated stubs import each other as ``from spectator.v1 import ...``, so the
# gen/ tree must be importable as a top-level package root.
_GEN = os.path.join(os.path.dirname(os.path.abspath(__file__)), "gen")
if _GEN not in sys.path:
    sys.path.insert(0, _GEN)

import grpc  # noqa: E402
from spectator.v1 import worker_pb2 as pb  # noqa: E402
from spectator.v1 import worker_pb2_grpc as pb_grpc  # noqa: E402

log = logging.getLogger(__name__)


class UnknownCommand(Exception):
    def __init__(self, kind: str | None) -> None:
        super().__init__(f"unknown command payload: {kind!r}")
        self.kind = kind


class CommandDispatcher:
    """Routes a ``Command`` oneof to the matching handler callback."""

    def __init__(
        self,
        on_start_spectate: Callable[[pb.StartSpectate], None],
        on_stop_spectate: Callable[[pb.StopSpectate], None],
        on_steam_guard: Callable[[pb.SubmitSteamGuardCode], None],
        on_list_friends: Callable[[pb.ListFriends], None],
    ) -> None:
        self._handlers = {
            "start_spectate": (on_start_spectate, lambda c: c.start_spectate),
            "stop_spectate": (on_stop_spectate, lambda c: c.stop_spectate),
            "steam_guard": (on_steam_guard, lambda c: c.steam_guard),
            "list_friends": (on_list_friends, lambda c: c.list_friends),
        }

    def dispatch(self, command: pb.Command) -> None:
        kind = command.WhichOneof("payload")
        entry = self._handlers.get(kind)
        if entry is None:
            raise UnknownCommand(kind)
        handler, extract = entry
        handler(extract(command))


class GrpcClient:
    """Manages the WorkerSession bidirectional stream.

    Events to send are placed on an internal queue; the request iterator drains
    it. Received commands are passed to ``dispatcher``.
    """

    def __init__(self, address: str, worker_id: str, dispatcher: CommandDispatcher) -> None:
        self._address = address
        self._worker_id = worker_id
        self._dispatcher = dispatcher
        self._outbox: "queue.Queue[pb.WorkerEvent | None]" = queue.Queue()

    def send(self, event: pb.WorkerEvent) -> None:
        event.worker_id = self._worker_id
        self._outbox.put(event)

    def _requests(self) -> Iterator[pb.WorkerEvent]:
        while True:
            event = self._outbox.get()
            if event is None:  # sentinel → close the stream
                return
            yield event

    def run(self) -> None:
        """Open the stream and block, dispatching commands until it ends."""
        log.info("connecting to control plane at %s", self._address)
        with grpc.insecure_channel(self._address) as channel:
            stub = pb_grpc.ControlPlaneServiceStub(channel)
            for command in stub.WorkerSession(self._requests()):
                try:
                    self._dispatcher.dispatch(command)
                except UnknownCommand:
                    log.warning("ignoring unknown command: %s", command.WhichOneof("payload"))

    def close(self) -> None:
        self._outbox.put(None)
