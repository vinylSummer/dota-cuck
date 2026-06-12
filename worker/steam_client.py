"""Warm python-steam session: friend listing with online + in-match status.

The worker runs as a single process (Python 3.10, protobuf-3.20 line), so this
talks to python-steam directly — no subprocess bridge. python-steam is imported
lazily inside methods, so the pure helpers (``derive_status``) and their tests
don't need the library installed.

The session is kept warm across ``list_friends`` calls (relogin via the saved
login_key when possible) and dropped before GUI Steam takes the account to
spectate. GC match-ID resolution and sentry handling (step 7) will extend this
module.

NOTE: the python-steam calls here (login/relogin, friend enumeration,
persona-state fields) must be validated against a live Steam login on the
server — see Known Risks in CLAUDE.md. The pure decision logic is
``derive_status``.
"""

from __future__ import annotations

# Steam app id for Dota 2. A friend whose currently-played game matches it is
# treated as in a match.
DOTA2_APP_ID = 570

# EPersonaState.Offline == 0; any other value is some online state.
PERSONA_STATE_OFFLINE = 0


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


class SteamSession:
    def __init__(self, credential_location: str = ".") -> None:
        self._cred_loc = credential_location
        self._client = None
        self._username: str | None = None

    def _ensure_client(self):
        if self._client is None:
            from steam.client import SteamClient

            client = SteamClient()
            # Required for sentry files to be written/persisted.
            client.set_credential_location(self._cred_loc)
            self._client = client
        return self._client

    def list_friends(self, username: str, password: str, sentry: str | None = None):
        """Return (owner_steam_id, [friend dict]). Logs in if the warm session
        isn't already authenticated as this user."""
        client = self._ensure_client()

        if not getattr(client, "logged_on", False) or self._username != username:
            self._login(client, username, password)
            self._username = username

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

    def _login(self, client, username: str, password: str) -> None:
        from steam.enums import EResult

        if client.relogin_available:
            result = client.relogin()
        else:
            result = client.login(username=username, password=password)

        if result == EResult.OK:
            return
        if result == EResult.AccountLoginDeniedNeedTwoFactor:
            raise SteamGuardRequired("MOBILE")
        if result == EResult.AccountLogonDenied:
            raise SteamGuardRequired("EMAIL")
        raise LoginError(f"login failed: {result!r}")

    def logout(self) -> None:
        """Drop the session (e.g. before GUI Steam takes the account to spectate)."""
        if self._client is not None and getattr(self._client, "logged_on", False):
            self._client.logout()
        self._username = None
