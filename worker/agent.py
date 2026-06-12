"""Worker entry point and state-machine driver.

V1 skeleton: connects to the control plane, advances the worker state machine
in response to commands, and logs every transition. No Steam, Dota, or FFmpeg
behaviour yet — those handlers (steam_client, dota_client, ffmpeg) are stubs.
"""

from __future__ import annotations

import logging
import os
import sys
import uuid

import state_machine as sm
from grpc_client import CommandDispatcher, GrpcClient

_GEN = os.path.join(os.path.dirname(os.path.abspath(__file__)), "gen")
if _GEN not in sys.path:
    sys.path.insert(0, _GEN)

from spectator.v1 import worker_pb2 as pb  # noqa: E402

log = logging.getLogger("worker.agent")

# Map the pure state-machine states onto the generated proto enum so we can
# report them in StatusUpdate events.
_PROTO_STATE = {
    sm.State.STOPPED: pb.STOPPED,
    sm.State.STARTING: pb.STARTING,
    sm.State.IDLE: pb.IDLE,
    sm.State.SPECTATING: pb.SPECTATING,
    sm.State.STOPPING: pb.STOPPING,
}


class Agent:
    def __init__(self, address: str, worker_id: str) -> None:
        self.state = sm.State.STOPPED
        dispatcher = CommandDispatcher(
            on_start_spectate=self._on_start_spectate,
            on_stop_spectate=self._on_stop_spectate,
            on_steam_guard=self._on_steam_guard,
        )
        self._client = GrpcClient(address, worker_id, dispatcher)

    def _advance(self, event: sm.Event) -> None:
        try:
            new_state = sm.next_state(self.state, event)
        except sm.InvalidTransition as exc:
            log.warning("%s", exc)
            return
        log.info("state: %s --%s--> %s", self.state.name, event.name, new_state.name)
        self.state = new_state
        self._client.send(pb.WorkerEvent(status_update=pb.StatusUpdate(state=_PROTO_STATE[new_state])))

    # --- Command handlers (no-op stubs for the skeleton) ---

    def _on_start_spectate(self, cmd: pb.StartSpectate) -> None:
        log.info("StartSpectate: session=%s target=%s", cmd.session_id, cmd.target_steam_id)
        self._advance(sm.Event.START_SPECTATE)

    def _on_stop_spectate(self, _cmd: pb.StopSpectate) -> None:
        log.info("StopSpectate")
        self._advance(sm.Event.STOP_SPECTATE)

    def _on_steam_guard(self, cmd: pb.SubmitSteamGuardCode) -> None:
        log.info("SubmitSteamGuardCode: code=%s", "*" * len(cmd.code))

    def run(self) -> None:
        # WorkerReady must be the first message on the stream, ahead of the
        # StatusUpdate emitted by the STREAM_CONNECTED transition.
        self._client.send(pb.WorkerEvent(ready=pb.WorkerReady()))
        self._advance(sm.Event.STREAM_CONNECTED)
        self._client.run()


def main() -> None:
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )
    address = os.environ.get("CONTROL_PLANE_ADDR", "control-plane:42010")
    worker_id = os.environ.get("WORKER_ID", str(uuid.uuid4()))
    log.info("worker %s starting", worker_id)
    Agent(address, worker_id).run()


if __name__ == "__main__":
    main()
