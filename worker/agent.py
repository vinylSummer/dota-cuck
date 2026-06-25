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
from dota_client import DotaClient, SpectateError
from grpc_client import CommandDispatcher, GrpcClient
from steam_client import LoginError, SteamGuardRequired, SteamSession
from steam_gui import SteamGui

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


def link_ok_event(
    request_id: str, owner_steam_id: str, refresh_token: str
) -> pb.WorkerEvent:
    """Build a successful LinkResult event reporting the account's Steam ID and
    the refresh token the control plane encrypts and persists."""
    return pb.WorkerEvent(
        link_result=pb.LinkResult(
            request_id=request_id,
            owner_steam_id=owner_steam_id,
            refresh_token=refresh_token,
        )
    )


def qr_challenge_event(request_id: str, challenge_url: str) -> pb.WorkerEvent:
    """Build a SteamQrChallenge event carrying the URL to render as a QR code."""
    return pb.WorkerEvent(
        qr_challenge=pb.SteamQrChallenge(
            request_id=request_id, challenge_url=challenge_url
        )
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
        self,
        address: str,
        worker_id: str,
        steam_session: SteamSession | None = None,
        dota_client: DotaClient | None = None,
        steam_gui: SteamGui | None = None,
    ) -> None:
        self.state = sm.State.STOPPED
        self._steam = steam_session if steam_session is not None else SteamSession()
        # The Dota GUI automation and the GUI Steam bring-up. Both optional in V1: a
        # worker with no DotaClient resolves the match id then stops at the handoff;
        # with a DotaClient but no SteamGui it drives spectate against an
        # already-running Dota (the harness brings Steam/Dota up). With both, the
        # worker performs the full STARTING bring-up itself.
        self._dota = dota_client
        self._steam_gui = steam_gui
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
                cmd.target_steam_id, cmd.refresh_token
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

        # --- C: drive the GUI spectate path (dashboard -> WATCH FRIEND LIVE ->
        # player view). The friend's persona name (for the OCR row match) comes
        # from the warm session that just resolved the match id. ---
        if self._dota is None:
            log.info("StartSpectate: no DotaClient wired; GUI spectate + FFmpeg "
                     "pending (step 8/11 bring-up)")
            return

        try:
            target_name = self._steam.persona_name(cmd.target_steam_id)
        except Exception as exc:  # noqa: BLE001 — non-fatal; spectate will fail to locate
            target_name = ""
            log.warning("StartSpectate persona-name lookup failed: %s", exc)

        # Full worker bring-up (only when a SteamGui is wired): hand the account from
        # the warm python-steam session to the GUI Steam client, then launch Dota to
        # the dashboard. Without it, Dota is assumed already up (the harness path).
        if self._steam_gui is not None:
            try:
                self._steam.logout()  # drop the warm session before GUI Steam takes the account
                self._steam_gui.ensure_logged_in()
            except Exception as exc:  # noqa: BLE001 — any login failure is fatal
                log.warning("StartSpectate GUI Steam login failed: %s", exc)
                self._fail_spectate("STEAM_GUI_LOGIN_FAILED", str(exc))
                return
            try:
                self._dota.launch_dota()
                self._dota.wait_for_dota_window()
            except SpectateError as exc:
                self._fail_spectate(exc.code, str(exc))
                return
            except Exception as exc:  # noqa: BLE001 — any launch failure is fatal
                log.warning("StartSpectate Dota launch failed: %s", exc)
                self._fail_spectate("DOTA_LAUNCH_FAILED", str(exc))
                return

        try:
            self._dota.spectate(target_name)
        except SpectateError as exc:
            log.warning("StartSpectate GUI spectate failed (%s): %s", exc.code, exc)
            self._fail_spectate(exc.code, str(exc))
            return
        except Exception as exc:  # noqa: BLE001 — any Dota/runtime failure is fatal
            log.warning("StartSpectate GUI spectate error: %s", exc)
            self._fail_spectate("SPECTATE_FAILED", str(exc))
            return
        log.info("StartSpectate: live spectate established (player view)")

        # --- step 9 continues here: start FFmpeg (x11grab -> hevc_nvenc -> SRT),
        # then emit StreamStarted to drive STARTING -> SPECTATING. ---

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
        # Empty credentials => QR link (mobile-authenticator accounts); credentials
        # present => the email-only / no-2FA fallback. Both yield a refresh token.
        qr_mode = not cmd.steam_username and not cmd.steam_password
        log.info(
            "LinkAccount: request=%s mode=%s",
            cmd.request_id,
            "qr" if qr_mode else "credentials",
        )

        def on_challenge(url: str) -> None:
            log.info("LinkAccount: qr challenge")
            self._client.send(qr_challenge_event(cmd.request_id, url))

        def on_guard(guard_type: str) -> None:
            log.info("LinkAccount: steam guard required (%s)", guard_type)
            self._client.send(steam_guard_event(cmd.request_id, guard_type))

        try:
            if qr_mode:
                owner, refresh_token = self._steam.begin_qr_link(
                    on_challenge=on_challenge
                )
            else:
                owner, refresh_token = self._steam.begin_credentials_link(
                    cmd.steam_username, cmd.steam_password, on_guard=on_guard
                )
        except Exception as exc:  # noqa: BLE001 — any Steam/runtime failure becomes an error event
            log.warning("LinkAccount failed: %s", exc)
            event = link_error_event(cmd.request_id, exc)
        else:
            event = link_ok_event(cmd.request_id, owner, refresh_token)
        self._client.send(event)

    def _on_list_friends(self, cmd: pb.ListFriends) -> None:
        # Run off the command-stream thread so a slow Steam reply doesn't block
        # receiving further commands.
        threading.Thread(target=self._list_friends, args=(cmd,), daemon=True).start()

    def _list_friends(self, cmd: pb.ListFriends) -> None:
        log.info("ListFriends: request=%s", cmd.request_id)
        try:
            owner, friends = self._steam.list_friends(cmd.refresh_token)
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

    # Full Dota bring-up (GUI Steam login + launch + GUI spectate) requires the
    # desktop/Xorg/uinput container stack, so it's opt-in until that lands (step 11).
    # WORKER_DOTA_BRINGUP=1 wires the GUI automation; otherwise the worker serves
    # friends/link/match-id and stops spectate at the handoff.
    dota = gui = None
    if os.environ.get("WORKER_DOTA_BRINGUP") == "1":
        dota = DotaClient()
        dota.setup()  # create the uinput devices before Dota launches (Source 2 enumerates at start)
        gui = SteamGui()
        log.info("Dota bring-up enabled (GUI Steam + spectate automation)")

    Agent(address, worker_id, dota_client=dota, steam_gui=gui).run()


if __name__ == "__main__":
    main()
