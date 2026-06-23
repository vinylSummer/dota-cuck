#!/bin/bash
# Full Xfce4 session on :99 with its own dbus session bus. xfwm4 + the Xfce compositor are
# what let Steam's CEF login popup actually render (bare Xorg / lone openbox did not).
set -e
export DISPLAY=:99
export XDG_RUNTIME_DIR="/tmp/xdg-worker"
mkdir -p "$XDG_RUNTIME_DIR"
chmod 700 "$XDG_RUNTIME_DIR"

# Wait for Xorg :99 to accept connections before starting the session.
for _ in $(seq 1 60); do
    xset q >/dev/null 2>&1 && break
    sleep 1
done
xset q >/dev/null 2>&1 || { echo "start-xfce: Xorg :99 never came up" >&2; exit 1; }

exec dbus-launch --exit-with-session startxfce4
