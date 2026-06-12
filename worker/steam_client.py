"""Warm python-steam session: account link (sentry establishment) and friend
listing with online + in-match status.

The worker runs as a single process (Python 3.10, protobuf-3.20 line), so this
talks to python-steam directly — no subprocess bridge. python-steam is imported
lazily inside methods, so the pure helper (``derive_status``) and its tests don't
need the library installed.

**Sentry only — the login_key is never persisted.** ``set_credential_location``
makes python-steam write the Steam Guard *machine auth* (sentry) to the
steam-data volume, which suppresses the guard on later logins. We deliberately do
not subscribe to / persist ``new_login_key`` (a full relogin secret); the control
plane re-supplies the password from its in-memory key cache when a cold login is
needed. See the sentry-only decision in the project notes.

The interactive Steam Guard flow lives here: ``link`` drives a login that pauses
on a guard challenge, invokes ``on_guard`` (so the agent can emit
``SteamGuardRequired`` upstream), and resumes once ``submit_guard_code`` delivers
a code. The session is kept warm across calls and dropped before GUI Steam takes
the account to spectate.

NOTE: the python-steam calls here (login, sentry behavior, friend enumeration,
persona-state fields) must be validated against a live Steam login on the
server — see Known Risks in CLAUDE.md. The pure decision logic is
``derive_status`` and the login-result classification in ``_classify_login``.
"""

from __future__ import annotations

import threading

# Steam app id for Dota 2. A friend whose currently-played game matches it is
# treated as in a match.
DOTA2_APP_ID = 570

# EPersonaState.Offline == 0; any other value is some online state.
PERSONA_STATE_OFFLINE = 0

# How long a login waits for a Steam Guard code before giving up.
GUARD_CODE_TIMEOUT_SECONDS = 300


class SteamGuardRequired(Exception):
    """Login needs a Steam Guard code. guard_type is "EMAIL" or "MOBILE"."""

    def __init__(self, guard_type: str = "EMAIL") -> None:
        super().__init__(f"steam guard required ({guard_type})")
        self.guard_type = guard_type


class LoginError(Exception):
    """Login failed for a non-recoverable reason (bad password, etc.)."""


def derive_status(persona_state: int, game_app_id: int | None) -> tuple[bool, bool]:
    """Map a friend's raw persona state and currently-played app id to
    (online, in_match)."""
    online = persona_state != PERSONA_STATE_OFFLINE
    in_match = game_app_id == DOTA2_APP_ID
    return online, in_match


def _classify_login(result) -> tuple[str, str | None]:
    """Classify a python-steam login EResult into a verdict the login loop acts
    on. Returns one of:

      ("ok", None)                  — logged in
      ("guard", "MOBILE"|"EMAIL")   — a Steam Guard code is required; the second
                                      item also names the login kwarg to resend
                                      it under (two_factor_code / auth_code)
      ("fail", None)                — non-recoverable

    Kept pure (no I/O) so the branch logic is unit-testable; the EResult values
    themselves come from python-steam and are validated on-server.
    """
    from steam.enums import EResult

    if result == EResult.OK:
        return "ok", None
    # A required-or-mismatched mobile authenticator code → re-prompt MOBILE.
    if result in (EResult.AccountLoginDeniedNeedTwoFactor, EResult.TwoFactorCodeMismatch):
        return "guard", "MOBILE"
    # A required-or-rejected emailed code → re-prompt EMAIL (Steam re-sends one).
    if result == EResult.AccountLogonDenied:
        return "guard", "EMAIL"
    return "fail", None


# Login kwarg python-steam expects each guard code under.
_GUARD_KWARG = {"MOBILE": "two_factor_code", "EMAIL": "auth_code"}


class SteamSession:
    def __init__(self, credential_location: str = ".") -> None:
        self._cred_loc = credential_location
        self._client = None
        self._username: str | None = None
        # Steam Guard code handoff: the login loop blocks on _guard_event until
        # submit_guard_code delivers a code. A round-trip per challenge, so the
        # event is cleared after each wait to support a wrong-code retry.
        self._guard_event = threading.Event()
        self._guard_lock = threading.Lock()
        self._guard_code: str | None = None

    def _ensure_client(self):
        if self._client is None:
            from steam.client import SteamClient

            client = SteamClient()
            # Required for the sentry file to be written/persisted (sentry-only;
            # we never persist login_key).
            client.set_credential_location(self._cred_loc)
            self._client = client
        return self._client

    def submit_guard_code(self, code: str) -> None:
        """Deliver a Steam Guard code to a login paused awaiting one."""
        with self._guard_lock:
            self._guard_code = code
        self._guard_event.set()

    def _wait_for_guard_code(self) -> str:
        if not self._guard_event.wait(GUARD_CODE_TIMEOUT_SECONDS):
            raise LoginError("steam guard code not submitted in time")
        with self._guard_lock:
            code = self._guard_code
            self._guard_code = None
        self._guard_event.clear()
        if not code:
            raise LoginError("empty steam guard code")
        return code

    def link(self, username: str, password: str, on_guard=None) -> str:
        """Log in to establish the sentry and return the account's own
        SteamID64. ``on_guard(guard_type)`` is called when a Steam Guard code is
        required; the login then waits for ``submit_guard_code``."""
        client = self._ensure_client()
        self._ensure_logged_in(client, username, password, on_guard)
        return str(client.steam_id.as_64) if client.steam_id else ""

    def list_friends(self, username: str, password: str, sentry: str | None = None):
        """Return (owner_steam_id, [friend dict]). Logs in if the warm session
        isn't already authenticated as this user.

        No ``on_guard`` here: by the time friends are fetched the account has
        been linked (sentry established), so a guard challenge is unexpected and
        surfaces as ``SteamGuardRequired``. ``sentry`` is accepted for call-site
        compatibility but unused — the worker owns its sentry on the volume."""
        client = self._ensure_client()
        self._ensure_logged_in(client, username, password, on_guard=None)

        owner = str(client.steam_id.as_64) if client.steam_id else ""

        friends = []
        for user in client.friends:
            persona_state = int(getattr(user, "state", 0) or 0)
            # game_played_app_id is set when the friend is in a game; absent otherwise.
            played = user.get_ps("game_played_app_id") if hasattr(user, "get_ps") else None
            game_app_id = int(played) if played else None
            online, in_match = derive_status(persona_state, game_app_id)
            friends.append(
                {
                    "steam_id": str(user.steam_id.as_64),
                    "persona_name": user.name or "",
                    "online": online,
                    "in_match": in_match,
                }
            )
        return owner, friends

    def _ensure_logged_in(self, client, username: str, password: str, on_guard) -> None:
        if getattr(client, "logged_on", False) and self._username == username:
            return
        self._login_interactive(client, username, password, on_guard)
        self._username = username

    def _login_interactive(self, client, username: str, password: str, on_guard) -> None:
        """Credential login, relying on the persisted sentry to skip the guard.
        When the guard still fires (first login on this machine, or sentry not
        yet trusted), pause: call ``on_guard`` and resume with the submitted
        code. ``on_guard=None`` means non-interactive — raise instead."""
        code_kwargs: dict[str, str] = {}
        while True:
            result = client.login(username=username, password=password, **code_kwargs)
            verdict, guard_type = _classify_login(result)
            if verdict == "ok":
                return
            if verdict == "fail":
                raise LoginError(f"login failed: {result!r}")
            # verdict == "guard"
            if on_guard is None:
                raise SteamGuardRequired(guard_type)
            on_guard(guard_type)
            code = self._wait_for_guard_code()
            code_kwargs = {_GUARD_KWARG[guard_type]: code}

    def logout(self) -> None:
        """Drop the session (e.g. before GUI Steam takes the account to spectate)."""
        if self._client is not None and getattr(self._client, "logged_on", False):
            self._client.logout()
        self._username = None
