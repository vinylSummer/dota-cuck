#!/usr/bin/env bash
# V5 — Dota spectate console command + camera follow, headless on :99.
#
# THE BIGGEST UNKNOWN. Finds the exact, reproducible sequence to join a live match as a
# spectator by WatchableGameID and follow a player, with no interactive TTY — the in-game
# console is driven via xdotool on :99. Task C copies whatever this proves, verbatim.
#
# Preconditions:
#   - V4 passed: GUI Steam sentry persisted at $STEAM_HOME_DIR (silent login).
#   - Dota installed at /fard/steam/dota (V2).
#   - A FRESH live WatchableGameID. Re-run v3_gc_matchid.py while a friend is in a live,
#     watchable, public match; it writes ~/.dota-validation.matchid, which this reads.
#
# What it does on :99: launch Dota via Steam (-novid -console -nosound), open the console
# (backquote) with xdotool, try the spectate-join + camera-follow command candidates, take
# screenshots, and grab a short NVENC clip to prove the captured frame is a MOVING live
# match (not the menu). Record the working command sequence in docs/validation-results.md.
#
#   scripts/validation/v5_spectate.sh                 # match id from ~/.dota-validation.matchid
#   MATCH_ID=29885xxxx scripts/validation/v5_spectate.sh   # or pass one explicitly
set -euo pipefail
cd "$(dirname "$0")/../.."
set -a; . ~/.dota-validation.env; set +a
: "${STEAM_USER:?set STEAM_USER in ~/.dota-validation.env}"
: "${STEAM_PASS:?set STEAM_PASS in ~/.dota-validation.env}"

MATCH_ID=${MATCH_ID:-$(cat ~/.dota-validation.matchid 2>/dev/null || true)}
: "${MATCH_ID:?no match id — run v3_gc_matchid.py first (writes ~/.dota-validation.matchid) or pass MATCH_ID=}"
echo "== spectating WatchableGameID=$MATCH_ID =="

STEAMHOME=${STEAM_HOME_DIR:-/fard/steam/steamhome}
SHOTDIR="$STEAMHOME/v5-shots"
mkdir -p "$STEAMHOME" "$SHOTDIR"

echo "== building images =="
docker build -f scripts/validation/Dockerfile.xtest -t dota-xtest .
docker build -f scripts/validation/Dockerfile.steam -t dota-steam .

docker rm -f dota-v5 >/dev/null 2>&1 || true
docker run --rm --name dota-v5 --network host --gpus all \
    -e NVIDIA_DRIVER_CAPABILITIES=all \
    -e HOME="$STEAMHOME" \
    -e STEAM_USER="$STEAM_USER" -e STEAM_PASS="$STEAM_PASS" \
    -e MATCH_ID="$MATCH_ID" \
    -v /fard/steam:/fard/steam \
    -v "$PWD/scripts/validation:/probe:ro" \
    dota-steam bash -c '
    set -e
    SHOTS="$HOME/v5-shots"; mkdir -p "$SHOTS"
    shot(){ DISPLAY=:99 ffmpeg -hide_banner -loglevel error -y -f x11grab -frames:v 1 -i :99 "$SHOTS/$1.png" 2>/dev/null || true; }

    echo "--- starting Xorg :99 ---"
    Xorg :99 -config /etc/X11/xorg.conf -noreset &
    for i in $(seq 1 30); do DISPLAY=:99 xset q >/dev/null 2>&1 && break; sleep 0.5; done
    DISPLAY=:99 xset q >/dev/null 2>&1 || { echo "Xorg :99 failed"; exit 1; }

    echo "=== GUI Steam silent login (reuse V4 sentry) ==="
    DISPLAY=:99 steam -no-browser -login "$STEAM_USER" "$STEAM_PASS" >/tmp/steam.log 2>&1 &
    for i in $(seq 1 40); do
        CLOG=$(find "$HOME/.steam" "$HOME/.local/share/Steam" -name connection_log.txt 2>/dev/null | head -1)
        [ -n "$CLOG" ] && grep -qiE "Logged On|LoggedOn" "$CLOG" 2>/dev/null && { echo "steam logged in"; break; }
        sleep 3
    done

    echo "=== launch Dota headless: -novid -console -nosound ==="
    # -applaunch routes through Steam so the install at /fard/steam/dota + entitlement apply.
    DISPLAY=:99 steam -applaunch 570 -novid -console -nosound -nopreload >/tmp/dota.log 2>&1 &
    echo "waiting for the Dota window to render on :99 ..."
    for i in $(seq 1 60); do        # Source2 first launch (shader compile) can be slow
        if DISPLAY=:99 xdotool search --name "Dota 2" >/dev/null 2>&1; then echo "Dota window present"; break; fi
        sleep 3
    done
    sleep 20                         # let the main menu settle
    shot 00-menu

    # Focus the Dota window so console keystrokes land in it.
    WIN=$(DISPLAY=:99 xdotool search --name "Dota 2" | head -1 || true)
    [ -n "$WIN" ] && DISPLAY=:99 xdotool windowactivate --sync "$WIN" 2>/dev/null || true

    console(){          # open console (grave), type a command, Enter, screenshot
        DISPLAY=:99 xdotool key --clearmodifiers grave; sleep 1
        DISPLAY=:99 xdotool type --clearmodifiers "$1"; sleep 0.5
        DISPLAY=:99 xdotool key --clearmodifiers Return; sleep 1
        DISPLAY=:99 xdotool key --clearmodifiers grave; sleep 1   # close console
        shot "$2"
    }

    echo "=== spectate-join candidates (the primary is dota_spectate_game) ==="
    console "dota_spectate_game $MATCH_ID" 01-spectate_game
    sleep 8
    shot 02-after-join

    echo "=== camera-follow candidates ==="
    console "dota_spectator_mode 1" 03-spectator_mode
    console "spec_player 0" 04-spec_player0
    console "dota_spectator_autofollow 1" 05-autofollow
    sleep 5
    shot 06-after-camera

    echo "=== prove a MOVING live render: 6s NVENC capture ==="
    DISPLAY=:99 ffmpeg -hide_banner -loglevel error -y \
        -f x11grab -r 60 -s 1280x720 -i :99 \
        -t 6 -c:v hevc_nvenc -preset p4 -b:v 4M /tmp/v5_capture.mp4 2>/tmp/ff.log || true
    SZ=$(stat -c%s /tmp/v5_capture.mp4 2>/dev/null || echo 0)
    cp /tmp/v5_capture.mp4 "$SHOTS/v5_capture.mp4" 2>/dev/null || true
    echo "capture bytes: $SZ"

    echo "--- /tmp/dota.log (tail) ---"; tail -25 /tmp/dota.log 2>/dev/null || true

    # Heuristic only: a non-trivial encode of a moving scene is much larger than a static
    # menu at the same settings. The human MUST confirm via the screenshots which command
    # actually joined the match and followed a player.
    if [ "$SZ" -gt 200000 ]; then
        echo "V5 LIKELY-PASS: captured a non-trivial render ($SZ bytes). Inspect $SHOTS to"
        echo "        confirm it is the live match (not the menu) and which command worked."
    else
        echo "V5 INCOMPLETE: capture small ($SZ bytes) — join likely did not take."
        echo "        Inspect $SHOTS / /tmp/dota.log; try the alternate join/camera candidates."
    fi
'
echo "== screenshots + capture persisted at $SHOTDIR =="
echo "== record the working launch opts + console sequence in docs/validation-results.md (V5) =="
