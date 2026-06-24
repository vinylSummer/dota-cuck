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
        --device /dev/uinput \
        --device-cgroup-rule 'c 13:* rmw' \
        -e NVIDIA_DRIVER_CAPABILITIES=all \
        -e GTK_A11Y=none \
        -e ENABLE_VNC="${ENABLE_VNC:-true}" \
        -e HOME="$STEAMHOME" \
        -v /fard/steam:/fard/steam \
        -v "$PWD/scripts/validation:/probe:ro" \
        dota-steam dumb-init -- supervisord -c /etc/supervisord.conf >/dev/null
}

find_clog() { find "${CLOG_GLOB[@]}" -name connection_log.txt 2>/dev/null | head -1; }

# connection_log.txt is append-only ACROSS runs (it holds "Logged On" lines from previous days),
# so grepping the whole file always matches a stale logon. mark_clog() records the file's line
# count right before this run's `steam` launch; logged_on() then inspects only the lines added
# since, and treats the run as logged on iff the most recent state transition in that region is a
# "Logged On" (not a later "Logged Off"/disconnect). Steam appends on a fresh start; if it ever
# truncates/rotates (new total < mark) we fall back to scanning the whole file.
CLOG_MARK=0
mark_clog() { local c; c=$(find_clog); CLOG_MARK=$([ -n "$c" ] && wc -l < "$c" 2>/dev/null || echo 0); }
logged_on() {
    local c total start last
    c=$(find_clog); [ -n "$c" ] || return 1
    total=$(wc -l < "$c" 2>/dev/null || echo 0); start=$CLOG_MARK
    [ "$total" -lt "$start" ] && start=0
    last=$(tail -n +$((start + 1)) "$c" 2>/dev/null | grep -ioE "Logged On|Logged Off" | tail -1)
    [ "$last" = "Logged On" ]
}

# Confirmed CM logon means the GUI Steam authenticated this run; Dota must NOT launch before this,
# or its steamclient.so reports no logged-on user and the client shows "LOST CONNECTION TO STEAM".
# After detection, settle briefly so the steamclient IPC / user session is ready before Dota inits.
LOGON_SETTLE="${LOGON_SETTLE:-10}"
wait_logged_on() {
    echo "== waiting for silent auto-login (this run only) =="
    for _ in $(seq 1 40); do
        logged_on && { echo "logged on; settling ${LOGON_SETTLE}s before Dota"; sleep "$LOGON_SETTLE"; return 0; }
        sleep 3
    done
    echo "WARNING: not logged on within window (check V4 persisted login)"; return 1
}

launch_steam_silent() {
    for _ in $(seq 1 60); do
        docker exec "$NAME" bash -lc 'DISPLAY=:99 wmctrl -m >/dev/null 2>&1' && break; sleep 2
    done
    mark_clog   # snapshot connection_log BEFORE launching, so logged_on() ignores prior runs
    docker exec -d "$NAME" runuser -u worker -- bash -lc \
        'export DISPLAY=:99 HOME=/fard/steam/steamhome XDG_RUNTIME_DIR=/tmp/xdg-worker; mkdir -p "$XDG_RUNTIME_DIR"; chmod 700 "$XDG_RUNTIME_DIR"; dbus-run-session -- steam -no-browser >"$HOME/v5-steam.log" 2>&1'
}

FIFO=/tmp/dota_uinput.fifo
VNC_FIFO=/tmp/vnc_input.fifo
# uinput devices we create + their target udev ID_INPUT_* tag (libinput classification hint).
UINPUT_NAMES="dota-spectate-uinput dota-vnc-kbd dota-vnc-mouse"

kill_dota() {
    # ALWAYS tear down the whole Dota tree before (re)launching — never leave a stale dota2
    # holding the Steam session (the account would show "in Dota"). The chain is
    # runuser->bash->srt-bwrap->pv-adverb->dota.sh->dota2; killing only dota2 leaves the sniper
    # container alive. Kill by EXACT comm names (never matches this script's own args, unlike
    # `pkill -f`, which self-matches on the install path). Killing srt-bwrap drops the container.
    docker exec "$NAME" bash -lc '
        for c in dota2 srt-bwrap pv-adverb pv-bwrap reaper; do pkill -9 -x "$c" 2>/dev/null; done
        sleep 3
        rm -f /tmp/source_engine_*.lock' 2>/dev/null || true
    # verify nothing Dota-related survives (steam client is left running for auth)
    if docker exec "$NAME" bash -lc 'ps -eo comm,args | grep -iE "dota2|/fard/steam/dota/game|run-in-sniper" | grep -v grep | grep -qv steamwebhelper'; then
        echo "WARNING: Dota processes still present after kill_dota"; return 1
    fi
    return 0
}

setup_uinput() {
    # Source 2 ignores XTEST synthetic events, so the console is driven through a real kernel
    # input device (/dev/uinput) — a PERSISTENT uinput keyboard daemon (uinput_daemon.py),
    # created BEFORE Dota launches and fed via $FIFO.
    #
    # The /dev/input/eventN node and the udev registration the device needs are now owned by
    # the supervised `udev` service (start-udev-auto.sh -> dumb-udev). dumb-udev provides the
    # /run/udev runtime that Source 2's libudev enumeration requires: without it dota2 busy-loops
    # on the udev-monitor netlink and never renders (the old mknod-only approach hit exactly
    # this). So here we just wait for dumb-udev to be up, start the daemon (whose device add
    # dumb-udev catches and registers), and confirm the node appeared — falling back to a
    # filtered mknod only if needed.
    docker exec "$NAME" bash -lc "[ -c /dev/uinput ]" \
        || { echo "WARNING: /dev/uinput missing (need --device /dev/uinput)"; return 1; }
    docker exec "$NAME" bash -lc "chmod 666 /dev/uinput; rm -f '$FIFO'"
    docker exec "$NAME" bash -lc "python3 -c 'import evdev'" \
        || { echo "WARNING: python3-evdev not importable"; return 1; }
    # Wait for the udev service to publish /run/udev (dumb-udev's ensure_udev_paths) so the
    # device add below is registered rather than racing an empty udev runtime.
    for _ in $(seq 1 20); do
        docker exec "$NAME" bash -lc '[ -e /run/udev/control ]' && break; sleep 1
    done
    docker exec "$NAME" bash -lc '[ -e /run/udev/control ]' \
        || echo "WARNING: /run/udev not present — is the udev service up? (check /tmp/udev.log)"
    docker exec -d "$NAME" runuser -u worker -- bash -lc \
        "python3 /usr/local/bin/uinput_daemon.py '$FIFO' >/tmp/uinput_daemon.log 2>&1"
    # The VNC input bridge: route x11vnc input through real uinput devices (Source 2 rejects the
    # XTEST x11vnc injects by default). Pre-create the FIFO as the FIFO *type* (so x11vnc's
    # `-pipeinput reopen` forwarder can't leave a regular file in its place) and start the
    # persistent daemon that owns dota-vnc-kbd + dota-vnc-mouse. x11vnc is bounced in the Xorg
    # restart below, so its forwarder reconnects to this FIFO afterward.
    docker exec "$NAME" runuser -u worker -- bash -lc \
        "rm -f '$VNC_FIFO'; mkfifo '$VNC_FIFO'; chmod 666 '$VNC_FIFO'" 2>/dev/null || true
    docker exec -d "$NAME" runuser -u worker -- bash -lc \
        "python3 /usr/local/bin/vnc_input_daemon.py '$VNC_FIFO' >/tmp/vnc_input_daemon.log 2>&1"
    # Daemons register the devices in /sys; dumb-udev then creates /dev/input/eventN + the
    # /run/udev/data entries. Wait for all three to appear (in /sys, then with a node), falling
    # back to our own filtered mknod.
    for name in $UINPUT_NAMES; do
        for _ in $(seq 1 15); do
            docker exec "$NAME" bash -lc "grep -lqs '$name' /sys/class/input/*/device/name" && break
            sleep 1
        done
    done
    for _ in $(seq 1 10); do
        docker exec "$NAME" bash -lc "
            want=\"$UINPUT_NAMES\"; missing=0
            for n in \$want; do
                got=0
                for d in /sys/class/input/event*; do
                    [ \"\$(cat \"\$d/device/name\" 2>/dev/null)\" = \"\$n\" ] || continue
                    [ -e \"/dev/input/\$(basename \"\$d\")\" ] && got=1
                done
                [ \"\$got\" = 1 ] || missing=1
            done
            [ \"\$missing\" = 0 ]" && break
        sleep 1
    done
    sync_input_nodes \
        && echo "uinput daemons up; nodes exposed for: $UINPUT_NAMES" \
        || { echo "WARNING: could not expose all uinput device nodes"; }
    # dumb-udev mis-classifies our virtual devices (e.g. ID_INPUT_JOYSTICK); libinput then refuses
    # them ("not using input device ... tagged as Joystick") even with the forced dota- InputClass.
    # Rewrite each device's udev db entry to the correct ID_INPUT_* (keyboard or mouse) so libinput
    # classifies and binds it on the Xorg (re)enumeration below.
    docker exec "$NAME" bash -lc '
        tag() { case "$1" in dota-vnc-mouse) echo MOUSE;; *) echo KEYBOARD;; esac; }
        for d in /sys/class/input/event*; do
            n=$(cat "$d/device/name" 2>/dev/null) || continue
            case "$n" in dota-spectate-uinput|dota-vnc-kbd|dota-vnc-mouse) ;; *) continue;; esac
            t=$(tag "$n"); f="/run/udev/data/c$(cat "$d/dev")"
            if [ -f "$f" ]; then sed -i "s/ID_INPUT_JOYSTICK/ID_INPUT_$t/" "$f"; fi
            grep -q "ID_INPUT_$t" "$f" 2>/dev/null \
                || printf "E:ID_INPUT=1\nE:ID_INPUT_%s=1\nG:seat\nG:uaccess\n" "$t" > "$f"
            echo "retagged $f ($n) as $t"
        done'
    # The device was created AFTER Xorg started, so Xorg's startup udev enumeration never saw it
    # and (with no CAP_NET_ADMIN for the hotplug netlink broadcast) it won't auto-add live. Mirror
    # docker-steam-headless: restart Xorg once now that the device + udev entry exist, so its fresh
    # startup enumeration claims it and the 99-dota-uinput InputClass binds it to libinput. Safe
    # here — Steam/Dota aren't launched yet. Bounce xfce/x11vnc too (they hold the old X session).
    echo "== restarting Xorg so it enumerates the uinput devices (libinput bind) =="
    docker exec "$NAME" supervisorctl restart xorg xfce x11vnc >/dev/null 2>&1 || true
    for _ in $(seq 1 30); do
        docker exec "$NAME" bash -lc 'DISPLAY=:99 xset q >/dev/null 2>&1' && break; sleep 1
    done
    local missing=0
    for name in $UINPUT_NAMES; do
        if docker exec "$NAME" bash -lc "DISPLAY=:99 xinput list 2>/dev/null | grep -q '$name'"; then
            echo "xinput: $name attached"
        else
            echo "WARNING: $name NOT in xinput list — its input will not reach Dota"
            missing=1
        fi
    done
    [ "$missing" = 0 ] || docker exec "$NAME" bash -lc 'DISPLAY=:99 xinput list 2>&1' || true
}

sync_input_nodes() {
    # Fallback for the supervised udev service: mknod the /dev/input/eventN node for ONLY our named
    # uinput devices (CAP_MKNOD is granted by default), in case dumb-udev missed the add event.
    # Deliberately skip every other device in /sys (host Power Button / HD-Audio leaking through
    # the shared /sys would otherwise be exposed and re-trigger the udev-enumeration hang).
    docker exec "$NAME" bash -lc "
        want=\"$UINPUT_NAMES\"; mkdir -p /dev/input; ok=0
        for d in /sys/class/input/event*; do
            n=\$(cat \"\$d/device/name\" 2>/dev/null) || continue
            case \" \$want \" in *\" \$n \"*) ;; *) continue;; esac
            ev=\$(basename \"\$d\"); path=\"/dev/input/\$ev\"
            if [ -e \"\$path\" ]; then chmod 0666 \"\$path\"; ok=\$((ok+1)); continue; fi
            IFS=: read -r major minor < \"\$d/dev\"
            mknod \"\$path\" c \"\$major\" \"\$minor\" && chmod 0666 \"\$path\" && ok=\$((ok+1))
        done
        [ \"\$ok\" -ge 1 ]"
}

launch_dota() {
    # steamcmd's `force_install_dir` produces a FLAT install (no steamapps/common/<installdir>/),
    # so the GUI client's `-applaunch 570` can't see it. Instead launch Dota directly through the
    # install's own sniper wrapper (run-in-sniper -> the same _v2-entry-point Steam uses per
    # toolmanifest.vdf), with the GUI Steam client running only for auth (SteamAPI_Init reads
    # SteamAppId and connects to it). This keeps steamcmd managing the install (autoupdates).
    echo "== launching Dota via run-in-sniper (steamcmd-managed install + GUI Steam auth) =="
    # The in-game console is the only reliable way to drive Source 2 headless (XTEST is ignored;
    # the Panorama dashboard eats generic key binds, but `toggleconsole` is engine-special and
    # reaches the engine). autoexec binds grave -> toggleconsole so the uinput daemon can open the
    # console and type the in-session camera commands (dota_spectator_mode / spec_player / spec_mode)
    # AFTER the GUI spectate join. `+exec autoexec` loads it at startup.
    docker exec "$NAME" runuser -u worker -- bash -lc 'cat > /fard/steam/dota/game/dota/cfg/autoexec.cfg <<CFG
con_enable 1
bind "\`" "toggleconsole"
CFG'
    for attempt in 1 2 3; do
        kill_dota || true
        : > "$STEAMHOME/v5-dota.log"
        docker exec -d "$NAME" runuser -u worker -- bash -lc \
            'export DISPLAY=:99 HOME=/fard/steam/steamhome XDG_RUNTIME_DIR=/tmp/xdg-worker SteamAppId=570 SteamGameId=570; cd /fard/steam/dota; ./run-in-sniper -- /fard/steam/dota/game/dota.sh -novid -console -condebug -nosound -nopreload +developer 1 +exec autoexec >>"$HOME/v5-dota.log" 2>&1'
        # The sniper chain (srt-bwrap -> pv-adverb -> dota.sh) takes ~15-20s before the dota2
        # process itself appears, so wait for SteamAPI_Init / the denial before judging liveness.
        outcome=pending
        for _ in $(seq 1 20); do
            if grep -q "denied appID 570" "$STEAMHOME/v5-dota.log" 2>/dev/null; then outcome=denied; break; fi
            grep -q "SteamAPI_Init().*OK" "$STEAMHOME/v5-dota.log" 2>/dev/null && { outcome=auth_ok; break; }
            sleep 2
        done
        if [ "$outcome" = denied ]; then
            # post-silent-login license-load race: ConnectToGlobalUser denied appID 570. Wait
            # for the Steam client to finish syncing licenses and retry the launch.
            echo "attempt $attempt: Steam licenses not ready (denied appID 570); waiting 20s + retrying"
            sleep 20; continue
        fi
        echo "Dota authenticated (SteamAPI_Init OK); waiting for window (Vulkan pipeline compile, slow) ..."
        for _ in $(seq 1 90); do
            docker exec "$NAME" bash -lc "DISPLAY=:99 xdotool search --class dota2 >/dev/null 2>&1" && { echo "Dota window present"; return 0; }
            docker exec "$NAME" bash -lc "pgrep -x dota2 >/dev/null || pgrep -x srt-bwrap >/dev/null" || break
            sleep 5
        done
        echo "WARNING: no Dota window this attempt; see $STEAMHOME/v5-dota.log"
    done
    echo "WARNING: Dota did not come up after retries; check login/license state"; return 1
}

shot() {
    docker exec "$NAME" bash -lc \
        "DISPLAY=:99 ffmpeg -hide_banner -loglevel error -y -f x11grab -i :99 -frames:v 1 '$STEAMHOME/v5-shots/$1.png'" 2>/dev/null || true
}

console() {  # focus Dota (X window mgmt), then push a console command through the persistent uinput device
    # Window activation is a WM request (works over X); the keystrokes go through the kernel input
    # device the daemon holds open. Writing a line to $FIFO triggers grave/type/enter/grave.
    docker exec "$NAME" bash -lc "
        export DISPLAY=:99
        WIN=\$(xdotool search --class dota2 | head -1)
        [ -n \"\$WIN\" ] && xdotool windowactivate --sync \"\$WIN\" 2>/dev/null || true"
    # The FIFO is owned by the worker user in sticky /tmp, so the kernel's protected_fifos blocks
    # even root from writing it — write as the owner.
    docker exec "$NAME" runuser -u worker -- bash -lc "printf '%s\n' '$1' > '$FIFO'"
    sleep 4   # let the daemon type the sequence + the game react before the screenshot
    shot "$2"
}

case "${1:-}" in
up)
    build
    start_desktop
    setup_uinput || true       # persistent virtual keyboard, created BEFORE Dota launches
    launch_steam_silent
    if wait_logged_on; then
        launch_dota || true    # kills any stale Dota first; retries the license-load race
    else
        echo "ABORT: GUI Steam never confirmed logon — not launching Dota (would show LOST CONNECTION TO STEAM)"; exit 1
    fi
    shot "00-after-launch"
    echo "Dota launch attempted. Inspect $SHOTDIR/00-after-launch.png, then: $0 spectate"
    ;;
input)
    # Validation-only: build + desktop + setup_uinput, then stop. Proves the three uinput devices
    # bind via libinput (xinput list) WITHOUT signing the Steam account into Dota. Use this to
    # debug the input path in isolation before a full `manual`/`up` run.
    build
    start_desktop
    setup_uinput || true
    echo "== input path brought up (no Steam/Dota). Inspect: =="
    echo "   docker exec $NAME bash -lc 'DISPLAY=:99 xinput list'"
    echo "   docker exec $NAME tail -n 30 /tmp/vnc_input_daemon.log"
    ;;
manual)
    # Same bring-up as `up`, but hands the session to the operator for an interactive run:
    # VNC input is routed through the libinput-bound uinput devices (setup_uinput), so manual
    # mouse + keyboard actually reach Dota (not XTEST, which Source 2 drops). No auto-spectate.
    build
    start_desktop
    setup_uinput || true
    launch_steam_silent
    if wait_logged_on; then
        launch_dota || true
    else
        echo "ABORT: GUI Steam never confirmed logon — not launching Dota (would show LOST CONNECTION TO STEAM)"; exit 1
    fi
    shot "00-after-launch"
    HOSTREF=$(hostname -f 2>/dev/null || hostname)
    cat <<EOF

================  MANUAL VNC SESSION READY  ================
Dota is up at the dashboard (inspect $SHOTDIR/00-after-launch.png, or OCR it:
    scripts/validation/ocr.sh 00-after-launch).

1. From your laptop, open an SSH tunnel to this server:
       ssh -L 5900:localhost:5900 -L 6080:localhost:6080 $HOSTREF
2. Connect a VNC viewer to   localhost:5900   (no password),
   or open                    http://localhost:6080/vnc.html   in a browser.
3. Your mouse + keyboard are routed through uinput, so they reach Dota:
   - move the mouse: the cursor should TRACK (confirms the abs pointer bound)
   - click to dismiss the first-login modals (Dota Plus / party / behavior summary)
   - press backtick (\`) to toggle the console; type a spectate command
   Find the exact path to a live spectate and report it back.

Verify the input devices bound:
    docker exec $NAME bash -lc 'DISPLAY=:99 xinput list'
    docker exec $NAME tail -n 30 /tmp/vnc_input_daemon.log   # raw pipeinput format + activity

Tear down when done:  $0 down
===========================================================
EOF
    ;;
spectate)
    # No console command joins a live match by id (dota_spectate_game does NOT exist). Spectating
    # a friend is GC-mediated through the GUI: friends panel -> right-click the friend in a live
    # match -> Spectate. gui_spectate.py drives that as an OCR-gated click state machine over the
    # libinput-bound uinput devices (mouse via the VNC FIFO, console via the uinput FIFO), then
    # grabs the NVENC capture itself. TARGET_NAME is the friend's persona (what ListFriends
    # returns); it's matched against the friends panel by OCR. MATCH_ID is no longer a join arg —
    # it's only the precondition that the target is actually in a live, watchable match.
    : "${TARGET_NAME:?set TARGET_NAME=<friend persona> (the friend in a live match to spectate)}"
    docker exec "$NAME" bash -lc "pgrep -af uinput_daemon >/dev/null" || setup_uinput || true
    docker exec "$NAME" bash -lc "pgrep -af vnc_input_daemon >/dev/null" \
        || { echo "WARNING: vnc_input_daemon not running — mouse clicks won't reach Dota"; }
    echo "== GUI-spectating friend '$TARGET_NAME' (match $MATCH_ID must be live) =="
    shot "01-menu"
    # Run the state machine as the worker user so it can write the worker-owned input FIFOs
    # (protected_fifos) and reach :99. It writes its own gate screenshots + NVENC capture to SHOTDIR.
    docker exec "$NAME" runuser -u worker -- bash -lc \
        "export DISPLAY=:99 HOME=$STEAMHOME SCREEN=1280x720 SHOTDIR='$SHOTDIR'; \
         python3 /usr/local/bin/gui_spectate.py spectate --target-name '$TARGET_NAME'" \
        && rc=0 || rc=$?
    SZ=$(stat -c%s "$STEAMHOME/v5-shots/v5_capture.mp4" 2>/dev/null || echo 0)
    echo "spectate rc=$rc; capture bytes: $SZ"
    if [ "$SZ" -gt 200000 ]; then
        echo "V5 LIKELY-PASS: non-trivial render ($SZ bytes). Inspect $SHOTDIR to confirm it is"
        echo "        the live match (not the menu); record the working camera command."
    else
        echo "V5 INCOMPLETE: capture small ($SZ bytes). Inspect $SHOTDIR, the gate shots in"
        echo "        $SHOTDIR/gui_spectate/, and $STEAMHOME/v5-dota.log."
    fi
    ;;
down)
    kill_dota || true          # ensure no dota2 lingers holding the Steam session
    docker rm -f "$NAME" >/dev/null 2>&1 || true; echo "torn down." ;;
*)
    echo "usage: $0 {up|manual|input|spectate|down}"
    echo "  spectate needs TARGET_NAME=<friend persona>; MATCH_ID (from ~/.dota-validation.matchid"
    echo "  or env) is the precondition that the target is in a live match."; exit 2;;
esac
