"""Warm python-steam session: refresh-token acquisition (account link) and
friend listing with online + in-match status.

The worker runs as a single process (Python 3.10, protobuf-3.20 line), so this
talks to python-steam directly — no subprocess bridge. python-steam is imported
lazily inside methods, so the pure helpers (``derive_status``,
``steam_id_from_jwt``, ``classify_confirmation``) and their tests don't need the
library installed.

**Modern auth model — refresh tokens, not sentries.** Account link runs the
``IAuthenticationService`` handshake once to obtain a long-lived **refresh
token**, which the control plane encrypts and persists. Cold logins log onto the
CM with that token (``CMsgClientLogon.access_token``) and need **zero** Steam
Guard interaction until the token expires or is revoked. There is no sentry and
no ``login_key`` — neither is persisted, and ``new_login_key`` is not handled.

Two acquisition paths converge on the same refresh token:
  - **QR** (``begin_qr_link``): the worker opens a QR auth session and surfaces
    the challenge URL via ``on_challenge``; the user scans it with the Steam
    mobile app (the scan *is* the mobile confirmation). For accounts with the
    Steam Mobile Authenticator.
  - **Credentials** (``begin_credentials_link``): for email-only / no-2FA
    accounts that can't scan a QR. The password is RSA-encrypted to Steam during
    the handshake and never persisted; an emailed code (or a mobile TOTP) is
    driven through the same ``submit_guard_code`` handoff as before.

NOTE: the python-steam / WebAPI calls here (the IAuthenticationService handshake,
the token CM logon, friend enumeration, persona-state fields) must be validated
against a live Steam login on the server — see Known Risks in CLAUDE.md. The
pure decision logic (``derive_status``, ``steam_id_from_jwt``,
``classify_confirmation``) is unit-tested.
"""

from __future__ import annotations

import base64
import json
import threading
import time

# Steam app id for Dota 2. A friend whose currently-played game matches it is
# treated as in a match.
DOTA2_APP_ID = 570

# EPersonaState.Offline == 0; any other value is some online state.
PERSONA_STATE_OFFLINE = 0

# How long a login waits for a Steam Guard code before giving up.
GUARD_CODE_TIMEOUT_SECONDS = 300

# How long the whole IAuthenticationService poll loop waits for the user to
# confirm (scan the QR / approve in-app) before giving up.
AUTH_POLL_TIMEOUT_SECONDS = 300

# Default poll interval (seconds) if Steam doesn't return one.
DEFAULT_POLL_INTERVAL = 2.0

# IAuthenticationService lives on the public WebAPI host; the handshake is
# decoupled from the CM socket.
_AUTH_API_BASE = "https://api.steampowered.com/IAuthenticationService"

# Name reported to Steam for this device (shown in the user's authorized-devices
# list). Cosmetic.
_DEVICE_NAME = "dota-spectator-worker"

# EAuthSessionGuardType values (from steammessages_auth). Mirrored here as plain
# ints so ``classify_confirmation`` stays pure (no protobuf import).
GUARD_NONE = 1
GUARD_EMAIL_CODE = 2
GUARD_DEVICE_CODE = 3
GUARD_DEVICE_CONFIRMATION = 4
GUARD_EMAIL_CONFIRMATION = 5


class SteamGuardRequired(Exception):
    """Login needs a Steam Guard code. guard_type is "EMAIL" or "MOBILE"."""

    def __init__(self, guard_type: str = "EMAIL") -> None:
        super().__init__(f"steam guard required ({guard_type})")
        self.guard_type = guard_type


class LoginError(Exception):
    """Login failed for a non-recoverable reason (bad password, expired token)."""


def derive_status(persona_state: int, game_app_id: int | None) -> tuple[bool, bool]:
    """Map a friend's raw persona state and currently-played app id to
    (online, in_match)."""
    online = persona_state != PERSONA_STATE_OFFLINE
    in_match = game_app_id == DOTA2_APP_ID
    return online, in_match


def steam_id_from_jwt(token: str) -> str:
    """Read the SteamID64 from a Steam refresh/access token's JWT ``sub`` claim.

    Steam tokens are JWTs (header.payload.signature); the payload is a
    base64url-encoded JSON object whose ``sub`` is the account's SteamID64. Pure
    (no signature verification — we only read an identifier we already trust the
    transport for), so it is unit-tested. Returns "" if the token is malformed.
    """
    try:
        payload = token.split(".")[1]
        # base64url, no padding — pad to a multiple of 4 before decoding.
        payload += "=" * (-len(payload) % 4)
        claims = json.loads(base64.urlsafe_b64decode(payload))
    except (ValueError, IndexError, json.JSONDecodeError):
        return ""
    return str(claims.get("sub", ""))


def classify_confirmation(confirmation_types) -> str:
    """Decide what a credentials-login session needs next from its
    ``allowed_confirmations``. Pure (takes the raw guard-type ints), so the
    branch logic is unit-tested. Returns one of:

      "email"  — an emailed Steam Guard code is required (re-prompt EMAIL)
      "device" — a mobile-authenticator code (TOTP) is required (re-prompt MOBILE)
      "poll"   — an out-of-band confirmation (approve in-app / via email link);
                 nothing to submit, just poll until approved
      "none"   — no confirmation needed; poll straight through to the token
    """
    present = set(confirmation_types)
    if GUARD_EMAIL_CODE in present:
        return "email"
    if GUARD_DEVICE_CODE in present:
        return "device"
    if present & {GUARD_DEVICE_CONFIRMATION, GUARD_EMAIL_CONFIRMATION}:
        return "poll"
    return "none"


def _service_post(method: str, request, response_cls):
    """POST a serialized IAuthenticationService request and parse the protobuf
    response. The request protobuf is base64'd into ``input_protobuf_encoded``;
    the response body is raw serialized protobuf. Network — validated on-server.
    """
    import requests

    url = f"{_AUTH_API_BASE}/{method}/v1/"
    encoded = base64.b64encode(request.SerializeToString()).decode("ascii")
    resp = requests.post(url, data={"input_protobuf_encoded": encoded}, timeout=30)
    resp.raise_for_status()
    out = response_cls()
    out.ParseFromString(resp.content)
    return out


def _rsa_encrypt_password(password: str, mod_hex: str, exp_hex: str) -> str:
    """RSA-PKCS1v1.5 encrypt the password with Steam's per-account public key
    (hex modulus/exponent from GetPasswordRSAPublicKey), base64 for transport.
    Uses pycryptodomex, already a python-steam dependency."""
    from Cryptodome.Cipher import PKCS1_v1_5
    from Cryptodome.PublicKey import RSA

    key = RSA.construct((int(mod_hex, 16), int(exp_hex, 16)))
    cipher = PKCS1_v1_5.new(key)
    return base64.b64encode(cipher.encrypt(password.encode("utf-8"))).decode("ascii")


class SteamSession:
    def __init__(self, device_name: str = _DEVICE_NAME) -> None:
        self._device_name = device_name
        self._client = None
        # SteamID64 of the account the warm CM session is currently logged in as,
        # so we can skip re-login when the same token is reused.
        self._steam_id: str | None = None
        # Steam Guard code handoff: the credentials login blocks on _guard_event
        # until submit_guard_code delivers a code. A round-trip per challenge, so
        # the event is cleared after each wait to support a wrong-code retry.
        self._guard_event = threading.Event()
        self._guard_lock = threading.Lock()
        self._guard_code: str | None = None

    def _ensure_client(self):
        if self._client is None:
            from steam.client import SteamClient

            self._client = SteamClient()
        return self._client

    def submit_guard_code(self, code: str) -> None:
        """Deliver a Steam Guard code to a credentials login paused awaiting one."""
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

    # --- Refresh-token acquisition (account link) ---

    def begin_qr_link(self, on_challenge) -> tuple[str, str]:
        """Run a QR auth session. ``on_challenge(url)`` is called with the
        challenge URL to render (and again on each rotation). Blocks until the
        user confirms by scanning. Returns (owner_steam_id, refresh_token)."""
        from steam.protobufs import steammessages_auth_pb2 as auth

        req = auth.CAuthentication_BeginAuthSessionViaQR_Request()
        req.device_friendly_name = self._device_name
        req.platform_type = auth.k_EAuthTokenPlatformType_SteamClient
        req.device_details.device_friendly_name = self._device_name
        req.device_details.platform_type = auth.k_EAuthTokenPlatformType_SteamClient

        resp = _service_post(
            "BeginAuthSessionViaQR",
            req,
            auth.CAuthentication_BeginAuthSessionViaQR_Response,
        )
        if on_challenge and resp.challenge_url:
            on_challenge(resp.challenge_url)

        poll = self._poll_to_refresh_token(
            resp.client_id,
            resp.request_id,
            resp.interval,
            on_new_challenge=on_challenge,
        )
        return steam_id_from_jwt(poll.refresh_token), poll.refresh_token

    def begin_credentials_link(
        self, username: str, password: str, on_guard
    ) -> tuple[str, str]:
        """Run a credentials auth session for an account that can't scan a QR
        (email-only / no-2FA, or a TOTP account via code entry). The password is
        RSA-encrypted to Steam and never persisted. ``on_guard(guard_type)`` is
        called when a code is required; the login then waits for
        ``submit_guard_code``. Returns (owner_steam_id, refresh_token)."""
        from steam.protobufs import enums_pb2
        from steam.protobufs import steammessages_auth_pb2 as auth

        rsa = _service_post(
            "GetPasswordRSAPublicKey",
            auth.CAuthentication_GetPasswordRSAPublicKey_Request(account_name=username),
            auth.CAuthentication_GetPasswordRSAPublicKey_Response,
        )
        enc_password = _rsa_encrypt_password(
            password, rsa.publickey_mod, rsa.publickey_exp
        )

        req = auth.CAuthentication_BeginAuthSessionViaCredentials_Request()
        req.account_name = username
        req.encrypted_password = enc_password
        req.encryption_timestamp = rsa.timestamp
        req.platform_type = auth.k_EAuthTokenPlatformType_SteamClient
        req.persistence = enums_pb2.k_ESessionPersistence_Persistent
        req.device_friendly_name = self._device_name
        req.device_details.device_friendly_name = self._device_name
        req.device_details.platform_type = auth.k_EAuthTokenPlatformType_SteamClient

        resp = _service_post(
            "BeginAuthSessionViaCredentials",
            req,
            auth.CAuthentication_BeginAuthSessionViaCredentials_Response,
        )
        if not resp.client_id and not resp.steamid:
            raise LoginError("login failed: invalid credentials")

        verdict = classify_confirmation(
            c.confirmation_type for c in resp.allowed_confirmations
        )
        if verdict in ("email", "device"):
            guard_type = "EMAIL" if verdict == "email" else "MOBILE"
            code_type = (
                auth.k_EAuthSessionGuardType_EmailCode
                if verdict == "email"
                else auth.k_EAuthSessionGuardType_DeviceCode
            )
            if on_guard is None:
                raise SteamGuardRequired(guard_type)
            on_guard(guard_type)
            code = self._wait_for_guard_code()
            _service_post(
                "UpdateAuthSessionWithSteamGuardCode",
                auth.CAuthentication_UpdateAuthSessionWithSteamGuardCode_Request(
                    client_id=resp.client_id,
                    steamid=resp.steamid,
                    code=code,
                    code_type=code_type,
                ),
                auth.CAuthentication_UpdateAuthSessionWithSteamGuardCode_Response,
            )
        # "poll" (out-of-band confirmation) and "none" fall straight through.

        poll = self._poll_to_refresh_token(
            resp.client_id, resp.request_id, resp.interval
        )
        return steam_id_from_jwt(poll.refresh_token), poll.refresh_token

    def _poll_to_refresh_token(
        self, client_id, request_id, interval, on_new_challenge=None
    ):
        """Poll PollAuthSessionStatus until a refresh token is issued (the user
        confirmed) or the timeout elapses. Returns the poll response."""
        from steam.protobufs import steammessages_auth_pb2 as auth

        wait = interval or DEFAULT_POLL_INTERVAL
        deadline = time.monotonic() + AUTH_POLL_TIMEOUT_SECONDS
        while True:
            resp = _service_post(
                "PollAuthSessionStatus",
                auth.CAuthentication_PollAuthSessionStatus_Request(
                    client_id=client_id, request_id=request_id
                ),
                auth.CAuthentication_PollAuthSessionStatus_Response,
            )
            if resp.new_client_id:
                client_id = resp.new_client_id
            if on_new_challenge and resp.new_challenge_url:
                on_new_challenge(resp.new_challenge_url)
            if resp.refresh_token:
                return resp
            if time.monotonic() > deadline:
                raise LoginError("steam auth session timed out awaiting confirmation")
            time.sleep(wait)

    # --- Token CM login + friends ---

    def login_with_token(self, refresh_token: str) -> str:
        """Log the warm CM session on with a refresh token (zero Steam Guard).
        Returns the account's own SteamID64. Idempotent: a no-op when already
        logged in as the token's account."""
        steam_id = steam_id_from_jwt(refresh_token)
        client = self._ensure_client()
        if getattr(client, "logged_on", False) and self._steam_id == steam_id:
            return steam_id

        from steam.core.msg import MsgProto
        from steam.enums import EOSType, EResult
        from steam.enums.emsg import EMsg
        from steam.steamid import SteamID
        from steam.utils import ip4_to_int

        pre = client._pre_login()
        if pre != EResult.OK:
            raise LoginError(f"could not connect to steam: {pre!r}")

        # CM logon with an access (refresh) token: identity comes from the token,
        # carried in the header steamid; no password / login_key / sentry.
        message = MsgProto(EMsg.ClientLogon)
        message.header.steamid = SteamID(steam_id)
        message.body.protocol_version = 65580
        message.body.client_package_version = 1561159470
        message.body.client_os_type = EOSType.Windows10
        message.body.client_language = "english"
        message.body.supports_rate_limit_response = True
        message.body.chat_mode = client.chat_mode
        message.body.obfuscated_private_ip.v4 = (
            ip4_to_int(client.connection.local_address) ^ 0xF00DBAAD
        )
        message.body.access_token = refresh_token

        client.send(message)
        resp = client.wait_msg(EMsg.ClientLogOnResponse, timeout=30)
        result = EResult(resp.body.eresult) if resp else EResult.Fail
        if result != EResult.OK:
            raise LoginError(f"token login failed: {result!r}")
        client.sleep(0.5)
        self._steam_id = steam_id
        return steam_id

    def list_friends(self, refresh_token: str):
        """Return (owner_steam_id, [friend dict]). Logs the warm session on with
        the refresh token if it isn't already authenticated as that account."""
        client = self._ensure_client()
        owner = self.login_with_token(refresh_token)

        friends = []
        for user in client.friends:
            persona_state = int(getattr(user, "state", 0) or 0)
            # game_played_app_id is set when the friend is in a game; absent otherwise.
            played = (
                user.get_ps("game_played_app_id") if hasattr(user, "get_ps") else None
            )
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

    def logout(self) -> None:
        """Drop the session (e.g. before GUI Steam takes the account to spectate)."""
        if self._client is not None and getattr(self._client, "logged_on", False):
            self._client.logout()
        self._steam_id = None
