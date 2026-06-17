"""V4 phase 1 — the warm python-steam session leg of the dual-session handoff.

Logs in with python-steam (the same library + sentry-location convention the worker's
SteamSession uses), proves the session is live by counting friends, then logs out
cleanly. The point is to vacate the account so the GUI Steam client (phase 2, driven by
v4_dual_session.sh) can take it without a "logged in elsewhere" kick.

Steam Guard handoff (non-interactive, works over SSH): on a required code the probe
prints `GUARD_REQUIRED:<EMAIL|MOBILE>` and polls GUARD_FILE; drop the code there.

Env (from ~/.dota-validation.env): STEAM_USER, STEAM_PASS.
Prints PYTHON_STEAM_LOGGED_IN / PYTHON_STEAM_LOGGED_OUT markers the runner greps for.
"""

import os
import sys
import time

from steam.client import SteamClient
from steam.enums import EResult

GUARD_FILE = os.path.expanduser("~/.dota-validation.guard")
SENTRY_DIR = os.path.expanduser("~/.dota-validation-sentry")
GUARD_TIMEOUT = 300


def log(*a):
    print(*a, flush=True)


def wait_for_guard_code(kind):
    if os.path.exists(GUARD_FILE):
        os.remove(GUARD_FILE)
    log(f"GUARD_REQUIRED:{kind}")
    deadline = time.time() + GUARD_TIMEOUT
    while time.time() < deadline:
        if os.path.exists(GUARD_FILE):
            with open(GUARD_FILE) as f:
                code = f.read().strip()
            os.remove(GUARD_FILE)
            if code:
                log(f"guard code received ({len(code)} chars)")
                return code
        time.sleep(1)
    raise SystemExit("guard code not provided in time")


def do_login(client, user, password):
    code_kwargs = {}
    while True:
        r = client.login(username=user, password=password, **code_kwargs)
        if r == EResult.OK:
            return
        if r in (EResult.AccountLoginDeniedNeedTwoFactor, EResult.TwoFactorCodeMismatch):
            code_kwargs = {"two_factor_code": wait_for_guard_code("MOBILE")}
        elif r == EResult.AccountLogonDenied:
            code_kwargs = {"auth_code": wait_for_guard_code("EMAIL")}
        else:
            raise SystemExit(f"login failed: {r!r}")


def main():
    user = os.environ["STEAM_USER"]
    password = os.environ["STEAM_PASS"]

    client = SteamClient()
    client.set_credential_location(SENTRY_DIR)
    do_login(client, user, password)
    log(f"PYTHON_STEAM_LOGGED_IN as {client.steam_id} "
        f"({client.user.name if client.user else '?'})")

    # Prove the session is genuinely active before vacating it.
    client.wait_event("persona_state", timeout=10)
    time.sleep(2)
    log(f"friends visible: {len(list(client.friends))}")

    client.logout()
    log("PYTHON_STEAM_LOGGED_OUT")


if __name__ == "__main__":
    try:
        main()
    finally:
        sys.stdout.flush()
