#!/usr/bin/env bash
# V5 — Dota spectate (join a live WatchableGameID + camera follow), headless on :99.
#
# Builds on the V4 desktop stack (full in-image NVIDIA driver, fake-monitor xorg.conf,
# --shm-size=2g, system dbus, Xfce4) and the V4-persisted SILENT auto-login — no creds here.
# It launches Dota via the logged-in GUI Steam, opens the console with xdotool, runs the
# spectate-join + camera-follow command candidates, and grabs a short NVENC clip to prove a
# MOVING live match was captured. Task C copies whatever this proves, verbatim.
#
# Preconditions:
#   - V4 passed: $STEAM_HOME_DIR holds a logged-in Steam (silent auto-login).
#   - Dota present (V2 install at /fard/steam/dota) AND visible to the GUI Steam client
#     (see `wire_dota_library` — registers the library + lays out steamapps/common).
#   - A FRESH live WatchableGameID (re-run v3_gc_matchid.py; it writes ~/.dota-validation.matchid).
#
#   scripts/validation/v5_spectate.sh up            # desktop + silent login + launch Dota
#   scripts/validation/v5_spectate.sh spectate      # run the join+follow sequence, capture
#   scripts/validation/v5_spectate.sh down
set -euo pipefail
cd "$(dirname "$0")/../.."
set -a; . ~/.dota-validation.env; set +a

STEAMHOME=${STEAM_HOME_DIR:-/fard/steam/steamhome}
SHOTDIR="$STEAMHOME/v5-shots"
NAME=dota-v5
CLOG_GLOB=("$STEAMHOME/.steam" "$STEAMHOME/.local/share/Steam")
MATCH_ID=${MATCH_ID:-$(cat ~/.dota-validation.matchid 2>/dev/null || true)}
mkdir -p "$STEAMHOME" "$SHOTDIR"
chown 1000:1000 "$STEAMHOME" 2>/dev/null || true

build() {
    echo "== building images =="
    docker build -f scripts/validation/Dockerfile.xtest -t dota-xtest .
    docker build -f scripts/validation/Dockerfile.steam -t dota-steam .
}

start_desktop() {
    docker rm -f "$NAME" >/dev/null 2>&1 || true
    docker run -d --name "$NAME" --network host --gpus all \
        --security-opt seccomp=unconfined \
        --security-opt apparmor=unconfined \
        --shm-size=2g \
        -e NVIDIA_DRIVER_CAPABILITIES=all \
        -e GTK_A11Y=none \
        -e ENABLE_VNC="${ENABLE_VNC:-true}" \
        -e HOME="$STEAMHOME" \
        -v /fard/steam:/fard/steam \
        -v "$PWD/scripts/validation:/probe:ro" \
        dota-steam dumb-init -- supervisord -c /etc/supervisord.conf >/dev/null
}

find_clog() { find "${CLOG_GLOB[@]}" -name connection_log.txt 2>/dev/null | head -1; }
logged_on() { local c; c=$(find_clog); [ -n "$c" ] && grep -qiE "Logged On|LoggedOn" "$c" 2>/dev/null; }

wait_logged_on() {
    echo "== waiting for silent auto-login =="
    for _ in $(seq 1 40); do logged_on && { echo "logged on"; return 0; }; sleep 3; done
    echo "WARNING: not logged on within window (check V4 persisted login)"; return 1
}

launch_steam_silent() {
    for _ in $(seq 1 60); do
        docker exec "$NAME" bash -lc 'DISPLAY=:99 wmctrl -m >/dev/null 2>&1' && break; sleep 2
    done
    docker exec -d "$NAME" runuser -u worker -- bash -lc \
        'export DISPLAY=:99 HOME=/fard/steam/steamhome XDG_RUNTIME_DIR=/tmp/xdg-worker; mkdir -p "$XDG_RUNTIME_DIR"; chmod 700 "$XDG_RUNTIME_DIR"; dbus-run-session -- steam -no-browser >"$HOME/v5-steam.log" 2>&1'
}

launch_dota() {
    # steamcmd's `force_install_dir` produces a FLAT install (no steamapps/common/<installdir>/),
    # so the GUI client's `-applaunch 570` can't see it. Instead launch Dota directly through the
    # install's own sniper wrapper (run-in-sniper -> the same _v2-entry-point Steam uses per
    # toolmanifest.vdf), with the GUI Steam client running only for auth (SteamAPI_Init reads
    # SteamAppId and connects to it). This keeps steamcmd managing the install (autoupdates).
    echo "== launching Dota via run-in-sniper (steamcmd-managed install + GUI Steam auth) =="
    docker exec -d "$NAME" runuser -u worker -- bash -lc \
        'export DISPLAY=:99 HOME=/fard/steam/steamhome XDG_RUNTIME_DIR=/tmp/xdg-worker SteamAppId=570 SteamGameId=570; cd /fard/steam/dota; ./run-in-sniper -- /fard/steam/dota/game/dota.sh -novid -console -nosound -nopreload >"$HOME/v5-dota.log" 2>&1'
    echo "waiting for the Dota window on :99 (first launch compiles Vulkan pipelines, slow) ..."
    for _ in $(seq 1 120); do      # Fossilize shader precompile can take 5-15 min on first run
        docker exec "$NAME" bash -lc "DISPLAY=:99 xdotool search --name 'Dota 2' >/dev/null 2>&1" && { echo "Dota window present"; return 0; }
        docker exec "$NAME" bash -lc "pgrep -x dota2 >/dev/null" || { echo "dota2 exited; see $STEAMHOME/v5-dota.log"; return 1; }
        sleep 5
    done
    echo "WARNING: no Dota window within the window; check $STEAMHOME/v5-dota.log"; return 1
}

shot() {
    docker exec "$NAME" bash -lc \
        "DISPLAY=:99 ffmpeg -hide_banner -loglevel error -y -f x11grab -i :99 -frames:v 1 '$STEAMHOME/v5-shots/$1.png'" 2>/dev/null || true
}

console() {  # open console (grave), type cmd, Enter, close, screenshot
    docker exec "$NAME" bash -lc "
        WIN=\$(DISPLAY=:99 xdotool search --name 'Dota 2' | head -1)
        [ -n \"\$WIN\" ] && DISPLAY=:99 xdotool windowactivate --sync \"\$WIN\" 2>/dev/null || true
        DISPLAY=:99 xdotool key --clearmodifiers grave; sleep 1
        DISPLAY=:99 xdotool type --clearmodifiers '$1'; sleep 0.5
        DISPLAY=:99 xdotool key --clearmodifiers Return; sleep 1
        DISPLAY=:99 xdotool key --clearmodifiers grave; sleep 1" 2>/dev/null || true
    shot "$2"
}

case "${1:-}" in
up)
    build
    start_desktop
    launch_steam_silent
    wait_logged_on || true
    launch_dota || true
    shot "00-after-launch"
    echo "Dota launch attempted. Inspect $SHOTDIR/00-after-launch.png, then: $0 spectate"
    ;;
spectate)
    : "${MATCH_ID:?no match id — run v3_gc_matchid.py first or pass MATCH_ID=}"
    echo "== spectating WatchableGameID=$MATCH_ID =="
    shot "01-menu"
    console "dota_spectate_game $MATCH_ID" 02-spectate_game
    sleep 8; shot 03-after-join
    console "dota_spectator_mode 1" 04-spectator_mode
    console "spec_player 0" 05-spec_player0
    console "dota_spectator_autofollow 1" 06-autofollow
    sleep 5; shot 07-after-camera
    echo "== 6s NVENC capture to prove a moving live render =="
    docker exec "$NAME" bash -lc \
        "DISPLAY=:99 ffmpeg -hide_banner -loglevel error -y -f x11grab -r 60 -s 1280x720 -i :99 -t 6 -c:v hevc_nvenc -preset p4 -b:v 4M '$STEAMHOME/v5-shots/v5_capture.mp4'" 2>/dev/null || true
    SZ=$(stat -c%s "$STEAMHOME/v5-shots/v5_capture.mp4" 2>/dev/null || echo 0)
    echo "capture bytes: $SZ"
    if [ "$SZ" -gt 200000 ]; then
        echo "V5 LIKELY-PASS: non-trivial render ($SZ bytes). Inspect $SHOTDIR to confirm it is"
        echo "        the live match (not the menu) and which console command joined/followed."
    else
        echo "V5 INCOMPLETE: capture small ($SZ bytes). Inspect $SHOTDIR / $STEAMHOME/v5-dota.log."
    fi
    ;;
down)
    docker rm -f "$NAME" >/dev/null 2>&1 || true; echo "torn down." ;;
*)
    echo "usage: $0 {up|spectate|down}   (MATCH_ID from ~/.dota-validation.matchid or env)"; exit 2;;
esac
