"""GUI Steam client bring-up for the worker: silent auto-login before Dota launches.

The worker spectates through the *graphical* Steam client (SteamAPI_Init reads SteamAppId and
connects to it), which is a SEPARATE session from the warm python-steam one used for friends /
match-id. The GUI client cannot ingest the refresh token, so V4 established a one-time interactive
login persisted on the steam-data volume; subsequent worker runs just relaunch it and it
auto-logs-in silently. Dota must NOT launch until this run's CM logon is confirmed, or its
steamclient.so reports no logged-on user and the client shows "LOST CONNECTION TO STEAM".

Ported from v5_spectate.sh (launch_steam_silent / mark_clog / logged_on / wait_logged_on). The
container/Xorg/udev/dbus stack is the entrypoint's job (step 11); this module is the worker-user
piece: launch ``steam`` under a dbus session and watch ``connection_log.txt`` for this run's logon.
The log parsing (``is_logged_on``) is pure and unit-tested; the process/poll glue is validated live.
"""

from __future__ import annotations

import logging
import os
import subprocess
import time

log = logging.getLogger("worker.steam_gui")


# ============================== pure decision logic (unit-tested) ==============================

# connection_log.txt is append-only ACROSS runs (it holds "Logged On" lines from previous days), so
# scanning the whole file always matches a stale logon. The caller snapshots the line count right
# before launching `steam` (the mark); this inspects only the lines added since and reports logged-on
# iff the most recent state transition in that region is a "Logged On" (not a later "Logged Off").


def is_logged_on(text: str, mark: int) -> bool:
    """True iff the GUI Steam client logged on *after* line ``mark`` and has not since logged off.

    ``text`` is the full connection_log.txt; ``mark`` is its line count snapshotted before this run's
    launch. If the log is shorter than the mark (truncation/rotation) the whole file is scanned. Pure.
    """
    lines = text.splitlines()
    start = mark if 0 <= mark <= len(lines) else 0
    last = None
    for ln in lines[start:]:
        low = ln.lower()
        # a line can carry the phrase as a substring; take the last transition seen, in order.
        on = low.rfind("logged on")
        off = low.rfind("logged off")
        if on == -1 and off == -1:
            continue
        last = "ON" if on > off else "OFF"
    return last == "ON"


# ===================================== I/O: SteamGui =====================================

# Where the GUI client writes connection_log.txt, relative to the Steam HOME.
_CLOG_ROOTS = (".steam", ".local/share/Steam")
_CLOG_NAME = "connection_log.txt"


class SteamGuiError(Exception):
    """GUI Steam login did not confirm within the window."""


class SteamGui:
    """Launches the GUI Steam client (silent auto-login) and waits for this run's CM logon.

    Runs as the worker user inside the already-set-up desktop container (Xorg :99 + dbus available),
    so no runuser/docker-exec wrapping — just subprocess. Not unit-tested (process/FS glue); the pure
    ``is_logged_on`` it relies on is.
    """

    def __init__(self, display: str | None = None, home: str | None = None,
                 logon_settle_seconds: float = 10.0) -> None:
        self.display = display or os.environ.get("DISPLAY", ":99")
        self.home = home or os.environ.get("HOME", "/fard/steam/steamhome")
        self.logon_settle_seconds = logon_settle_seconds
        self._mark = 0

    def _find_connection_log(self) -> str | None:
        for root in _CLOG_ROOTS:
            base = os.path.join(self.home, root)
            for dirpath, _dirs, files in os.walk(base):
                if _CLOG_NAME in files:
                    return os.path.join(dirpath, _CLOG_NAME)
        return None

    def _read_log(self) -> str:
        path = self._find_connection_log()
        if not path:
            return ""
        try:
            with open(path, errors="replace") as f:
                return f.read()
        except OSError:
            return ""

    def _mark_log(self) -> None:
        """Snapshot the connection_log line count so is_logged_on ignores prior runs' logons."""
        self._mark = len(self._read_log().splitlines())

    def launch_silent(self) -> None:
        """Start the GUI Steam client under a private dbus session for a silent auto-login. Marks the
        connection log first so the subsequent wait only counts this run's logon."""
        xdg = os.environ.get("XDG_RUNTIME_DIR", "/tmp/xdg-worker")
        os.makedirs(xdg, exist_ok=True)
        os.chmod(xdg, 0o700)
        self._mark_log()  # BEFORE launch, so is_logged_on ignores prior runs
        env = {**os.environ, "DISPLAY": self.display, "HOME": self.home, "XDG_RUNTIME_DIR": xdg}
        logpath = os.path.join(self.home, "worker-steam.log")
        logf = open(logpath, "ab")  # noqa: SIM115 — handed to the child for its lifetime
        log.info("launching GUI Steam (silent auto-login)")
        subprocess.Popen(
            ["dbus-run-session", "--", "steam", "-no-browser"],
            env=env, stdout=logf, stderr=subprocess.STDOUT, start_new_session=True,
        )

    def wait_logged_on(self, timeout_seconds: float = 120.0, poll_seconds: float = 3.0) -> None:
        """Block until this run's CM logon is confirmed, then settle so the steamclient IPC/user
        session is ready before Dota inits. Raises SteamGuiError on timeout."""
        log.info("waiting for GUI Steam silent auto-login (this run only)")
        deadline = time.time() + timeout_seconds
        while time.time() < deadline:
            if is_logged_on(self._read_log(), self._mark):
                log.info("GUI Steam logged on; settling %.0fs before Dota", self.logon_settle_seconds)
                time.sleep(self.logon_settle_seconds)
                return
            time.sleep(poll_seconds)
        raise SteamGuiError("GUI Steam never confirmed logon (check the persisted V4 login)")

    def ensure_logged_in(self, timeout_seconds: float = 120.0) -> None:
        """Launch the GUI client and wait for this run's logon."""
        self.launch_silent()
        self.wait_logged_on(timeout_seconds=timeout_seconds)
