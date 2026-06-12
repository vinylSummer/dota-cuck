"""Worker entry point and state-machine driver.

Connects to the control plane, advances the worker state machine in response to
commands, and logs every transition. ListFriends is served from the warm
in-process python-steam session; the spectate handlers (Dota, FFmpeg) are still
stubs.
"""

from __future__ import annotations

import logging
import os
import sys
import threading
import uuid

import state_machine as sm
from grpc_client import CommandDispatcher, GrpcClient
from steam_client import LoginError, SteamGuardRequired, SteamSession

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


# Maps a Steam exception type to the FriendsResult error code reported upstream.
_FRIENDS_ERROR_CODE = {
    SteamGuardRequired: "STEAM_GUARD_REQUIRED",
    LoginError: "LOGIN_FAILED",
}


def friends_ok_event(request_id: str, owner_steam_id: str, friends: list[dict]) -> pb.WorkerEvent:
    """Build a successful FriendsResult event from the session's (owner, friends)
    return. Pure, so the proto mapping is unit-tested."""
    return pb.WorkerEvent(
        friends_result=pb.FriendsResult(
            request_id=request_id,
            owner_steam_id=owner_steam_id,
            friends=[
                pb.Friend(
                    steam_id=f.get("steam_id", ""),
                    persona_name=f.get("persona_name", ""),
                    online=bool(f.get("online", False)),
                    in_match=bool(f.get("in_match", False)),
                )
                for f in friends
            ],
        )
    )


def friends_error_event(request_id: str, exc: Exception) -> pb.WorkerEvent:
    """Build a failed FriendsResult event, mapping the exception to an error
    code. Unknown failures fall back to STEAM_ERROR."""
    code = _FRIENDS_ERROR_CODE.get(type(exc), "STEAM_ERROR")
    return pb.WorkerEvent(
        friends_result=pb.FriendsResult(
            request_id=request_id,
            error=pb.ErrorEvent(code=code, message=str(exc), fatal=False),
        )
    )


class Agent:
    def __init__(self, address: str, worker_id: str, steam_session: SteamSession | None = None) -> None:
        self.state = sm.State.STOPPED
        self._steam = steam_session if steam_session is not None else SteamSession()
        dispatcher = CommandDispatcher(
            on_start_spectate=self._on_start_spectate,
            on_stop_spectate=self._on_stop_spectate,
            on_steam_guard=self._on_steam_guard,
            on_list_friends=self._on_list_friends,
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

    def _on_list_friends(self, cmd: pb.ListFriends) -> None:
        # Run off the command-stream thread so a slow Steam reply doesn't block
        # receiving further commands.
        threading.Thread(
            target=self._list_friends, args=(cmd,), daemon=True
        ).start()

    def _list_friends(self, cmd: pb.ListFriends) -> None:
        log.info("ListFriends: request=%s", cmd.request_id)
        sentry = cmd.sentry_hash.decode("latin-1") if cmd.sentry_hash else None
        try:
            owner, friends = self._steam.list_friends(cmd.steam_username, cmd.steam_password, sentry)
        except Exception as exc:  # noqa: BLE001 — any Steam/runtime failure becomes an error event
            log.warning("ListFriends failed: %s", exc)
            event = friends_error_event(cmd.request_id, exc)
        else:
            event = friends_ok_event(cmd.request_id, owner, friends)
        self._client.send(event)

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
