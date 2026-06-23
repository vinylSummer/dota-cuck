#!/usr/bin/env bash
# V4 — headless GUI-Steam login via the docker-steam-headless desktop path.
#
# The modern Steam login CANNOT be driven headlessly: the CEF login popup never renders on
# bare Xorg (and `steam -login user pass` is ignored), and the refresh token cannot be seeded
# into the GUI client (its token store is encrypted with an unpublished scheme). The only
# viable path is a ONE-TIME interactive login on a real desktop, after which the client's own
# persisted (encrypted, machine-bound) token drives silent headless auto-login.
#
# This script replicates docker-steam-headless: a full Xfce4 session on the real NVIDIA Xorg
# :99 (xfwm4 + compositor make the CEF popup render), reachable over x11vnc/noVNC. It runs in
# two phases:
#   up        — start the desktop + Steam; operator logs in once over VNC (QR / 2FA)
#   status    — report whether Steam has logged in (loginusers.vdf + "Logged On")
#   autologin — PROOF: fresh container, VNC OFF, Steam with no creds -> must log on silently
#   down      — tear down
#
# The persisted login + both phases share $HOME on the ZFS dataset (same host => the
# machine-bound token decrypts). Record findings in docs/validation-results.md (V4 section).
#
#   scripts/validation/v4_dual_session.sh up         # then complete the login over VNC
#   scripts/validation/v4_dual_session.sh status
#   scripts/validation/v4_dual_session.sh autologin
#   scripts/validation/v4_dual_session.sh down
set -euo pipefail
cd "$(dirname "$0")/../.."
set -a; . ~/.dota-validation.env; set +a

STEAMHOME=${STEAM_HOME_DIR:-/fard/steam/steamhome}
SHOTDIR="$STEAMHOME/v4-shots"
NAME=dota-v4
# Steam writes its connection log under one of these (depends on install layout).
CLOG_GLOB=("$STEAMHOME/.steam" "$STEAMHOME/.local/share/Steam")

mkdir -p "$STEAMHOME" "$SHOTDIR"
# The steam-data volume must be owned by the in-container worker uid (1000) — it already is on
# wolf-den (vinyl=1000), but assert so a silent permission failure doesn't masquerade as a
# login failure.
chown 1000:1000 "$STEAMHOME" 2>/dev/null || true

build() {
    echo "== building images =="
    docker build -f scripts/validation/Dockerfile.xtest -t dota-xtest .
    docker build -f scripts/validation/Dockerfile.steam -t dota-steam .
}

# Start the supervisord desktop (Xorg -> Xfce -> VNC) detached. $1 = ENABLE_VNC value.
start_desktop() {
    local enable_vnc="$1"
    docker rm -f "$NAME" >/dev/null 2>&1 || true
    # seccomp/apparmor unconfined: Steam's bwrap sandbox needs unprivileged user+mount
    # namespaces, which the default Docker profiles block (the worker needs this too).
    docker run -d --name "$NAME" --network host --gpus all \
        --security-opt seccomp=unconfined \
        --security-opt apparmor=unconfined \
        --shm-size=2g \
        -e NVIDIA_DRIVER_CAPABILITIES=all \
        -e GTK_A11Y=none \
        -e ENABLE_VNC="$enable_vnc" \
        -e HOME="$STEAMHOME" \
        -v /fard/steam:/fard/steam \
        -v "$PWD/scripts/validation:/probe:ro" \
        dota-steam dumb-init -- supervisord -c /etc/supervisord.conf >/dev/null
}

# Launch the GUI Steam client as the non-root worker, inside the Xfce session on :99.
# $@ extra args (e.g. nothing, or a username to prefill). Logs to $STEAMHOME/v4-steam.log.
launch_steam() {
    # Wait until the Xfce session owns the display (a root window manager is present).
    for _ in $(seq 1 60); do
        docker exec "$NAME" bash -lc 'DISPLAY=:99 wmctrl -m >/dev/null 2>&1' && break
        sleep 2
    done
    echo "== launching GUI Steam as worker on :99 =="
    docker exec -d "$NAME" runuser -u worker -- bash -lc \
        'export DISPLAY=:99 HOME=/fard/steam/steamhome XDG_RUNTIME_DIR=/tmp/xdg-worker; \
         mkdir -p "$XDG_RUNTIME_DIR"; chmod 700 "$XDG_RUNTIME_DIR"; \
         dbus-run-session -- steam -no-browser '"$*"' >"$HOME/v4-steam.log" 2>&1'
}

find_clog() { find "${CLOG_GLOB[@]}" -name connection_log.txt 2>/dev/null | head -1; }

logged_on() {
    local clog; clog=$(find_clog)
    [ -n "$clog" ] && grep -qiE "Logged On|LoggedOn|Logon success" "$clog" 2>/dev/null
}

has_loginusers() {
    # Command substitution (not a `| head | grep` pipeline) avoids a pipefail false-negative:
    # head closing early sends find SIGPIPE, which pipefail would treat as failure.
    [ -n "$(find "${CLOG_GLOB[@]}" -name loginusers.vdf 2>/dev/null | head -1)" ]
}

shot() {
    docker exec "$NAME" bash -lc \
        "DISPLAY=:99 ffmpeg -hide_banner -loglevel error -y -f x11grab -i :99 -frames:v 1 '$STEAMHOME/v4-shots/$1.png'" \
        2>/dev/null || true
}

case "${1:-}" in
up)
    build
    start_desktop true
    launch_steam ""
    cat <<EOF

== Desktop up. Complete the ONE-TIME Steam login over VNC. ==
   From your machine, tunnel and open noVNC:
     ssh -L 6080:localhost:6080 <wolf-den>      then browse http://localhost:6080/vnc.html
   (or tunnel the raw VNC port:  ssh -L 5900:localhost:5900 <wolf-den>  -> VNC viewer localhost:5900)

   Steam is bootstrapping its real client on first run (downloads, slow). When the login
   window appears, sign in with the Steam Mobile app QR or your 2FA code.

   Then check:   scripts/validation/v4_dual_session.sh status
EOF
    ;;
status)
    for i in $(seq 1 5); do shot "status-$(printf %02d "$i")"; sleep 2; done
    echo "loginusers.vdf present: $(has_loginusers && echo yes || echo no)"
    if logged_on; then
        echo "V4 PHASE-1 PASS: GUI Steam logged in (per $(find_clog))."
        echo "  Now prove silent headless auto-login:  $0 autologin"
    else
        echo "Not logged in yet. Steam log tail:"
        tail -20 "$STEAMHOME/v4-steam.log" 2>/dev/null || true
        echo "Inspect screenshots in $SHOTDIR and complete the login over VNC, then re-run status."
    fi
    ;;
autologin)
    has_loginusers || { echo "No loginusers.vdf yet — run 'up' and complete the login first."; exit 1; }
    echo "== AUTO-LOGIN PROOF: fresh container, VNC OFF, no credentials =="
    # Truncate the connection log so any "Logged On" we detect is from THIS silent run, not the
    # earlier interactive login (the log persists on the steam-data volume).
    docker rm -f "$NAME" >/dev/null 2>&1 || true
    find "${CLOG_GLOB[@]}" -name connection_log.txt -exec sh -c ': > "$1"' _ {} \; 2>/dev/null || true
    start_desktop false
    launch_steam ""            # no -login, no creds — must use the persisted token
    ok=""
    for i in $(seq 1 40); do   # ~120s
        if logged_on; then ok=1; echo "GUI Steam logged on silently (per $(find_clog))"; break; fi
        if grep -qiE "two.?factor|guard|enter your|sign in" "$STEAMHOME/v4-steam.log" 2>/dev/null; then
            echo "WARNING: a login prompt appeared — auto-login did NOT take."
        fi
        sleep 3
    done
    shot "autologin-final"
    if [ -n "$ok" ]; then
        echo "V4 PASS: headless silent auto-login from the persisted token works."
    else
        echo "V4 FAIL: no silent login within the window. Steam log tail:"
        tail -25 "$STEAMHOME/v4-steam.log" 2>/dev/null || true
    fi
    ;;
down)
    docker rm -f "$NAME" >/dev/null 2>&1 || true
    echo "torn down."
    ;;
*)
    echo "usage: $0 {up|status|autologin|down}"; exit 2;;
esac
