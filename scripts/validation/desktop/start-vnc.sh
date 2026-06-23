#!/bin/bash
# x11vnc exposing the Xfce desktop on :99. Gated by ENABLE_VNC so the V4 auto-login proof can
# run with VNC off (idle) and still keep supervisord happy.
export DISPLAY=:99
if [ "${ENABLE_VNC:-true}" != "true" ]; then
    echo "start-vnc: ENABLE_VNC!=true, idling"
    exec sleep infinity
fi

for _ in $(seq 1 60); do
    xset q >/dev/null 2>&1 && break
    sleep 1
done

# -nopw: no VNC password; reachable only via SSH tunnel to wolf-den (validation only).
# -noshm: MIT-SHM fails with BadAccess across the root-Xorg / worker-client boundary in the
# container, which crashes x11vnc — disable shared memory.
exec x11vnc -display :99 -forever -shared -nopw -rfbport 5900 -noxdamage -noshm
