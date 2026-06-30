#!/usr/bin/env bash
# Worker agent launcher (supervised). Waits for the headless Xorg :99 to be up —
# the agent needs the display for screenshots/OCR and for Dota — then runs the
# uv-managed worker. CONTROL_PLANE_ADDR / MEDIAMTX_* / WORKER_DOTA_BRINGUP come
# from the container environment (see docker-compose.yml).
set -euo pipefail

export DISPLAY="${DISPLAY:-:99}"
export HOME="${HOME:-/fard/steam/steamhome}"

# Wait for Xorg (gated on input-bind, so by the time it answers the devices are bound).
for _ in $(seq 1 90); do
    if DISPLAY="$DISPLAY" xset q >/dev/null 2>&1; then
        break
    fi
    sleep 1
done

# Confirm libinput bound the worker's input devices before driving Dota; warn (don't
# block) so a connectivity/friends-only run still comes up if input isn't ready.
if ! DISPLAY="$DISPLAY" xinput list 2>/dev/null | grep -q dota-vnc-mouse; then
    echo "run-worker: WARNING dota-vnc-mouse not in xinput list — input may not reach Dota"
fi

cd /opt/worker
exec uv run --no-dev python agent.py
