#!/usr/bin/env bash
# V1 — Headless Xorg + NVIDIA GLX rendering inside a container.
#
# Builds the xtest image, starts Xorg on :99 with the worker's headless xorg.conf,
# and checks that glxinfo reports the NVIDIA GPU as the OpenGL renderer (not the
# llvmpipe software rasterizer). PASS proves the worker can render Dota on a headless
# GPU-backed display before any automation is written.
#
# Run from the repo root on the target host (vinyl is in the docker group):
#   scripts/validation/v1_headless_gpu.sh
set -euo pipefail
cd "$(dirname "$0")/../.."

echo "== building xtest image =="
docker build -f scripts/validation/Dockerfile.xtest -t dota-xtest .

echo "== nvidia-smi inside container =="
docker run --rm --gpus all -e NVIDIA_DRIVER_CAPABILITIES=all dota-xtest \
    nvidia-smi --query-gpu=name,driver_version --format=csv

echo "== starting Xorg :99 and probing glxinfo =="
docker run --rm --gpus all -e NVIDIA_DRIVER_CAPABILITIES=all dota-xtest bash -c '
    set -e
    Xorg :99 -config /etc/X11/xorg.conf -noreset &
    XPID=$!
    for i in $(seq 1 30); do
        DISPLAY=:99 xdpyinfo >/dev/null 2>&1 && break
        sleep 0.5
    done
    echo "--- glxinfo -B ---"
    DISPLAY=:99 glxinfo -B
    RENDERER=$(DISPLAY=:99 glxinfo -B | grep -i "OpenGL renderer" || true)
    kill $XPID 2>/dev/null || true
    echo "$RENDERER"
    if echo "$RENDERER" | grep -qi nvidia; then
        echo "V1 PASS: NVIDIA GLX rendering on headless :99"
    else
        echo "V1 FAIL: renderer is not NVIDIA (got: $RENDERER)"
        exit 1
    fi
'
