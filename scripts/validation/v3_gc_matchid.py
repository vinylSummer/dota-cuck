"""V3 — resolve a live Dota match ID for a friend currently in-game.

Exploratory probe (known-risks.md "GC match-ID query"). Logs in with python-steam,
finds friends playing Dota 2 (app 570), requests their rich presence, and reports any
watchable match ID Dota publishes (`WatchableGameID` in rich presence is the spectator
game id). Cross-checks by launching the Dota GC (python-dota2). Whatever method actually
yields a match id here becomes the basis for the worker's GC query (step 7 tail).

Steam Guard handoff (works over SSH, non-interactive): when a code is required the probe
prints `GUARD_REQUIRED:<TYPE>` and polls GUARD_FILE; write the code there to resume.

Env (from ~/.dota-validation.env): STEAM_USER, STEAM_PASS, TARGET_STEAM_ID (optional —
if unset, the first Dota-playing friend is used). Writes the resolved match id to
RESULT_FILE so V5 can pick it up.
"""

import os
import sys
import time

from steam.client import SteamClient
from steam.enums import EResult
from steam.enums.emsg import EMsg

DOTA2_APP_ID = 570
GUARD_FILE = os.path.expanduser("~/.dota-validation.guard")
RESULT_FILE = os.path.expanduser("~/.dota-validation.matchid")
GUARD_TIMEOUT = 300


def log(*a):
    print(*a, flush=True)


def wait_for_guard_code(kind):
    """Print a marker and block until the operator drops a code in GUARD_FILE."""
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
            log(f"logged on as {client.steam_id} ({client.user.name if client.user else '?'})")
            return
        if r in (EResult.AccountLoginDeniedNeedTwoFactor, EResult.TwoFactorCodeMismatch):
            code_kwargs = {"two_factor_code": wait_for_guard_code("MOBILE")}
        elif r == EResult.AccountLogonDenied:
            code_kwargs = {"auth_code": wait_for_guard_code("EMAIL")}
        else:
            raise SystemExit(f"login failed: {r!r}")


def dump_user(u):
    rp = dict(getattr(u, "rich_presence", {}) or {})
    log(f"  friend {u.steam_id.as_64} '{u.name}' state={int(getattr(u, 'state', 0) or 0)} "
        f"app={u.get_ps('game_played_app_id')}")
    if rp:
        log(f"    rich_presence={rp}")
    return rp


def main():
    user = os.environ["STEAM_USER"]
    password = os.environ["STEAM_PASS"]
    target = os.environ.get("TARGET_STEAM_ID", "").strip()

    client = SteamClient()
    client.set_credential_location(os.path.expanduser("~/.dota-validation-sentry"))
    do_login(client, user, password)

    # Let the friends list + persona states populate.
    client.wait_event("persona_state", timeout=10)
    time.sleep(3)

    friends = list(client.friends)
    log(f"friends: {len(friends)}")

    # Request persona state *with rich presence* for everyone, so WatchableGameID lands.
    ids = [u.steam_id for u in friends]
    if ids:
        # 0x200 = RichPresence, plus the usual presence/gameinfo flags.
        try:
            client.request_persona_state(ids, state_flags=0x35F | 0x200)
        except TypeError:
            client.request_persona_state(ids)
    time.sleep(5)

    dota_friends = []
    for u in friends:
        rp = dump_user(u)
        in_dota = u.get_ps("game_played_app_id") == DOTA2_APP_ID
        if target and str(u.steam_id.as_64) == target:
            dota_friends.insert(0, (u, rp))
        elif in_dota:
            dota_friends.append((u, rp))

    if not dota_friends:
        log("RESULT: no friend currently in a Dota match (and target not in-game)")
        log("        re-run while a friend is in a live, watchable public match")
        return

    log(f"=== {len(dota_friends)} candidate(s) in Dota ===")
    match_id = None
    for u, rp in dota_friends:
        wid = rp.get("WatchableGameID") or rp.get("watching_server")
        log(f"candidate {u.steam_id.as_64} '{u.name}' WatchableGameID={rp.get('WatchableGameID')} "
            f"watching_server={rp.get('watching_server')} param0={rp.get('param0')}")
        if rp.get("WatchableGameID") and not match_id:
            match_id = rp["WatchableGameID"]

    # Cross-check via the Dota GC.
    try:
        from dota2.client import Dota2Client

        dota = Dota2Client(client)
        gc_ready = client.wait_event  # placeholder
        ready = {"v": False}

        @dota.on("ready")
        def _r():
            ready["v"] = True

        dota.launch()
        for _ in range(30):
            if ready["v"]:
                break
            time.sleep(0.5)
        log(f"GC ready={ready['v']}")
        if ready["v"] and target:
            # request_player_info / spectator data — exploratory; print whatever returns.
            try:
                dota.request_player_info([int(target) & 0xFFFFFFFF])
                resp = dota.wait_event("player_info", timeout=10)
                log(f"GC player_info: {resp}")
            except Exception as e:  # noqa: BLE001
                log(f"GC player_info probe error: {e}")
    except Exception as e:  # noqa: BLE001
        log(f"GC cross-check error: {e}")

    if match_id:
        with open(RESULT_FILE, "w") as f:
            f.write(str(match_id))
        log(f"V3 PASS: resolved live match id {match_id} (written to {RESULT_FILE})")
    else:
        log("V3 PARTIAL: friend in Dota but no WatchableGameID in rich presence")
        log("  (match may be non-watchable / private lobby, or RP flag not honored)")


if __name__ == "__main__":
    try:
        main()
    finally:
        sys.stdout.flush()
