#!/usr/bin/env bash
# Worker agent launcher (supervised). Waits for the headless Xorg :99 to be up —
# the agent needs the display for screenshots/OCR and for Dota — then runs the
# uv-managed worker. CONTROL_PLANE_ADDR / MEDIAMTX_* / WORKER_DOTA_BRINGUP come
# from the container environment (see docker-compose.yml).
set -euo pipefail

export DISPLAY="${DISPLAY:-:99}"
export HOME="${HOME:-/fard/steam/steamhome}"

# Wait for Xorg (supervisord priority brings it up first, but startup races).
for _ in $(seq 1 60); do
    if DISPLAY="$DISPLAY" xset q >/dev/null 2>&1; then
        break
    fi
    sleep 1
done

cd /opt/worker
exec uv run --no-dev python agent.py
