#!/usr/bin/env bash
# V4 — dual-session handoff (python-steam -> GUI Steam) + GUI-Steam Steam Guard.
#
# Proves the worker's §4-step-2 handoff: python-steam and the GUI Steam client can't
# both hold the account at once, so the worker logs python-steam out before spectating.
# This probe runs phase 1 (python-steam login -> prove live -> logout) then phase 2
# (launch the GUI Steam client headless on :99 and log in with the same creds), and
# captures enough to answer the open questions in known-risks.md:
#   - Does GUI Steam's first login re-trigger Steam Guard / a mobile-confirmation TAP
#     (which a headless box cannot perform)?
#   - Where does GUI Steam write its (separate) sentry, and are later logins silent?
#   - Is only one session on the account at a time (no "logged in elsewhere" kick)?
#
# It cannot fully automate a one-time mobile TAP (that needs a human on the phone) — it
# surfaces the prompt and captures screenshots so you can complete it and record V4.
#
#   set -a; . ~/.dota-validation.env; set +a   # (the runner does this too)
#   scripts/validation/v4_dual_session.sh
#
# Record findings in docs/validation-results.md (V4 section).
set -euo pipefail
cd "$(dirname "$0")/../.."
set -a; . ~/.dota-validation.env; set +a
: "${STEAM_USER:?set STEAM_USER in ~/.dota-validation.env}"
: "${STEAM_PASS:?set STEAM_PASS in ~/.dota-validation.env}"

# GUI Steam config + both sentries persist here (host-visible on the ZFS dataset), so a
# second run can confirm the GUI-Steam login is silent. This is the steam-data location.
STEAMHOME=${STEAM_HOME_DIR:-/fard/steam/steamhome}
SHOTDIR="$STEAMHOME/v4-shots"
mkdir -p "$STEAMHOME" "$SHOTDIR"

echo "== building images =="
docker build -f scripts/validation/Dockerfile.xtest -t dota-xtest .
docker build -f scripts/validation/Dockerfile.steam -t dota-steam .

echo "== GUARD codes: drop into  $STEAMHOME/.dota-validation.guard  (container HOME) =="
echo "== screenshots will be written to  $SHOTDIR  for you to inspect =="

docker rm -f dota-v4 >/dev/null 2>&1 || true
docker run --rm --name dota-v4 --network host --gpus all \
    -e NVIDIA_DRIVER_CAPABILITIES=all \
    -e HOME="$STEAMHOME" \
    -e STEAM_USER="$STEAM_USER" -e STEAM_PASS="$STEAM_PASS" \
    -v /fard/steam:/fard/steam \
    -v "$PWD/scripts/validation:/probe:ro" \
    dota-steam bash -c '
    set -e
    echo "--- starting Xorg :99 ---"
    Xorg :99 -config /etc/X11/xorg.conf -noreset &
    for i in $(seq 1 30); do DISPLAY=:99 xset q >/dev/null 2>&1 && break; sleep 0.5; done
    DISPLAY=:99 xset q >/dev/null 2>&1 || { echo "Xorg :99 failed"; exit 1; }

    echo "=== PHASE 1: python-steam login -> logout (vacate the account) ==="
    /opt/steam-venv/bin/python /probe/v4_steam_phase.py
    echo "=== PHASE 1 done; pausing 5s before GUI Steam takes the account ==="
    sleep 5

    echo "=== PHASE 2: GUI Steam client login on :99 ==="
    SHOTS="$HOME/v4-shots"; mkdir -p "$SHOTS"
    # First run bootstraps the real client; -login attempts a headless login. A guard /
    # mobile-confirmation dialog (if any) renders on :99 and is caught in the screenshots.
    DISPLAY=:99 steam -no-browser -login "$STEAM_USER" "$STEAM_PASS" >/tmp/steam.log 2>&1 &
    STEAMPID=$!

    LOGIN_OK=""
    for i in $(seq 1 60); do      # ~180s: bootstrap download + login can be slow
        # Single-frame screenshot for the operator to inspect any guard/tap dialog.
        DISPLAY=:99 ffmpeg -hide_banner -loglevel error -y -f x11grab -frames:v 1 \
            -i :99 "$SHOTS/shot-$(printf %03d "$i").png" 2>/dev/null || true
        # GUI Steam logs its logon state changes; "Logon state changed" -> "Logged On".
        CLOG=$(find "$HOME/.steam" "$HOME/.local/share/Steam" -name "connection_log.txt" 2>/dev/null | head -1)
        if [ -n "$CLOG" ] && grep -qiE "Logged On|logon state.*LoggedOn|Logon success" "$CLOG" 2>/dev/null; then
            LOGIN_OK=1; echo "GUI_STEAM_LOGGED_IN (per $CLOG)"; break
        fi
        if grep -qiE "two.?factor|guard|confirm.*mobile|approve" /tmp/steam.log "$CLOG" 2>/dev/null; then
            echo "GUI_STEAM_GUARD_PROMPT_DETECTED — inspect $SHOTS and complete on your phone"
        fi
        sleep 3
    done

    echo "--- /tmp/steam.log (tail) ---"; tail -30 /tmp/steam.log 2>/dev/null || true
    echo "--- GUI Steam sentry / config files (for the steam-data volume) ---"
    find "$HOME/.steam" "$HOME/.local/share/Steam/config" \
        -maxdepth 4 -type f \( -name "*.vdf" -o -name "ssfn*" -o -name "config.vdf" \) 2>/dev/null | head -20

    kill $STEAMPID 2>/dev/null || true
    if [ -n "$LOGIN_OK" ]; then
        echo "V4 PASS (GUI Steam logged in headless). Confirm in docs whether a one-time"
        echo "        human mobile tap was needed and that a re-run is silent."
    else
        echo "V4 INCOMPLETE: GUI Steam not confirmed logged in within the window."
        echo "        Inspect $SHOTS + /tmp/steam.log; likely a mobile-confirmation tap is required."
    fi
'
echo "== screenshots persisted at $SHOTDIR =="
