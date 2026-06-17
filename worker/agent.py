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

# Maps a Steam exception type to the LinkResult error code. The interactive guard
# is driven via a callback, so SteamGuardRequired is not expected on this path.
_LINK_ERROR_CODE = {
    LoginError: "LOGIN_FAILED",
}

# Maps the SteamSession guard_type string to the proto enum.
_GUARD_TYPE = {
    "EMAIL": pb.EMAIL,
    "MOBILE": pb.MOBILE,
}


def friends_ok_event(
    request_id: str, owner_steam_id: str, friends: list[dict]
) -> pb.WorkerEvent:
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


def link_ok_event(request_id: str, owner_steam_id: str) -> pb.WorkerEvent:
    """Build a successful LinkResult event reporting the account's Steam ID."""
    return pb.WorkerEvent(
        link_result=pb.LinkResult(request_id=request_id, owner_steam_id=owner_steam_id)
    )


def link_error_event(request_id: str, exc: Exception) -> pb.WorkerEvent:
    """Build a failed LinkResult event. Unknown failures fall back to STEAM_ERROR."""
    code = _LINK_ERROR_CODE.get(type(exc), "STEAM_ERROR")
    return pb.WorkerEvent(
        link_result=pb.LinkResult(
            request_id=request_id,
            error=pb.ErrorEvent(code=code, message=str(exc), fatal=False),
        )
    )


def match_id_resolved_event(match_id: int, steam_id: str) -> pb.WorkerEvent:
    """Build a MatchIdResolved event. steam_id is the target being watched. Pure,
    so the proto mapping is unit-tested."""
    return pb.WorkerEvent(
        match_id_resolved=pb.MatchIdResolved(match_id=match_id, steam_id=steam_id)
    )


def steam_guard_event(request_id: str, guard_type: str) -> pb.WorkerEvent:
    """Build a SteamGuardRequired event correlated to its login request."""
    return pb.WorkerEvent(
        steam_guard=pb.SteamGuardRequired(
            request_id=request_id,
            guard_type=_GUARD_TYPE.get(guard_type, pb.STEAM_GUARD_TYPE_UNSPECIFIED),
        )
    )


class Agent:
    def __init__(
        self, address: str, worker_id: str, steam_session: SteamSession | None = None
    ) -> None:
        self.state = sm.State.STOPPED
        self._steam = steam_session if steam_session is not None else SteamSession()
        dispatcher = CommandDispatcher(
            on_start_spectate=self._on_start_spectate,
            on_stop_spectate=self._on_stop_spectate,
            on_steam_guard=self._on_steam_guard,
            on_list_friends=self._on_list_friends,
            on_link_account=self._on_link_account,
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
        self._client.send(
            pb.WorkerEvent(status_update=pb.StatusUpdate(state=_PROTO_STATE[new_state]))
        )

    # --- Command handlers (no-op stubs for the skeleton) ---

    def _on_start_spectate(self, cmd: pb.StartSpectate) -> None:
        # Run off the command-stream thread: match-id resolution polls rich
        # presence (and later C drives Dota/FFmpeg), so it must not stall the
        # command receive loop.
        threading.Thread(target=self._start_spectate, args=(cmd,), daemon=True).start()

    def _start_spectate(self, cmd: pb.StartSpectate) -> None:
        log.info(
            "StartSpectate: session=%s target=%s", cmd.session_id, cmd.target_steam_id
        )
        self._advance(sm.Event.START_SPECTATE)  # IDLE → STARTING

        # --- B: resolve the live watchable match id on the warm python-steam
        # session (before the dual-session handoff to GUI Steam) ---
        try:
            match_id = self._steam.resolve_match_id(
                cmd.target_steam_id, cmd.steam_username, cmd.steam_password
            )
        except Exception as exc:  # noqa: BLE001 — any Steam/runtime failure is fatal
            log.warning("StartSpectate match-id resolve failed: %s", exc)
            self._fail_spectate("MATCH_RESOLVE_FAILED", str(exc))
            return
        if not match_id:
            self._fail_spectate(
                "NO_WATCHABLE_MATCH", "target is not in a live watchable match"
            )
            return
        log.info("StartSpectate: resolved match_id=%s", match_id)
        self._client.send(match_id_resolved_event(match_id, cmd.target_steam_id))

        # --- C continues here: logout python-steam, GUI Steam, Dota launch,
        # spectate join + camera follow, FFmpeg, StreamStarted (step 9). ---

    def _fail_spectate(self, code: str, message: str) -> None:
        """Emit a fatal ErrorEvent and drive STARTING → STOPPING."""
        self._client.send(
            pb.WorkerEvent(error=pb.ErrorEvent(code=code, message=message, fatal=True))
        )
        self._advance(sm.Event.FATAL_ERROR)

    def _on_stop_spectate(self, _cmd: pb.StopSpectate) -> None:
        log.info("StopSpectate")
        self._advance(sm.Event.STOP_SPECTATE)

    def _on_steam_guard(self, cmd: pb.SubmitSteamGuardCode) -> None:
        log.info("SubmitSteamGuardCode: code=%s", "*" * len(cmd.code))
        # V1 has a single in-flight login, so the code routes to the one session
        # awaiting it; the command's request_id is carried for forward compat.
        self._steam.submit_guard_code(cmd.code)

    def _on_link_account(self, cmd: pb.LinkAccount) -> None:
        # Run off the command-stream thread: the login can block on an
        # interactive Steam Guard prompt without stalling other commands.
        threading.Thread(target=self._link_account, args=(cmd,), daemon=True).start()

    def _link_account(self, cmd: pb.LinkAccount) -> None:
        log.info("LinkAccount: request=%s user=%s", cmd.request_id, cmd.steam_username)

        def on_guard(guard_type: str) -> None:
            log.info("LinkAccount: steam guard required (%s)", guard_type)
            self._client.send(steam_guard_event(cmd.request_id, guard_type))

        try:
            owner = self._steam.link(
                cmd.steam_username, cmd.steam_password, on_guard=on_guard
            )
        except Exception as exc:  # noqa: BLE001 — any Steam/runtime failure becomes an error event
            log.warning("LinkAccount failed: %s", exc)
            event = link_error_event(cmd.request_id, exc)
        else:
            event = link_ok_event(cmd.request_id, owner)
        self._client.send(event)

    def _on_list_friends(self, cmd: pb.ListFriends) -> None:
        # Run off the command-stream thread so a slow Steam reply doesn't block
        # receiving further commands.
        threading.Thread(target=self._list_friends, args=(cmd,), daemon=True).start()

    def _list_friends(self, cmd: pb.ListFriends) -> None:
        log.info("ListFriends: request=%s", cmd.request_id)
        sentry = cmd.sentry_hash.decode("latin-1") if cmd.sentry_hash else None
        try:
            owner, friends = self._steam.list_friends(
                cmd.steam_username, cmd.steam_password, sentry
            )
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
