#!/usr/bin/env bash
# V6 (NVENC half) — FFmpeg x11grab on headless :99 → hevc_nvenc.
#
# Proves the worker can capture a GPU-backed headless Xorg display and H.265-encode it
# on the NVENC ASIC (the unknown: NVENC + x11grab on a virtual framebuffer). Renders
# glxgears on :99 as a moving source, captures 5s with the deployment.md FFmpeg sketch,
# and checks the encoder reported hevc_nvenc frames. The full SRT→mediamtx→browser leg is
# validated separately by v6_srt_mediamtx.sh once mediamtx is up.
#
#   scripts/validation/v6_nvenc.sh
set -euo pipefail
cd "$(dirname "$0")/../.."

docker build -f scripts/validation/Dockerfile.xtest -t dota-xtest .

docker run --rm --gpus all -e NVIDIA_DRIVER_CAPABILITIES=all dota-xtest bash -c '
    set -e
    Xorg :99 -config /etc/X11/xorg.conf -noreset &
    for i in $(seq 1 30); do DISPLAY=:99 xdpyinfo >/dev/null 2>&1 && break; sleep 0.5; done
    # Moving source so the encoder has real frames to crunch.
    DISPLAY=:99 glxgears >/dev/null 2>&1 &
    sleep 1
    echo "--- ffmpeg x11grab -> hevc_nvenc (5s) ---"
    DISPLAY=:99 ffmpeg -hide_banner -y \
        -f x11grab -r 60 -s 1280x720 -i :99 \
        -t 5 -c:v hevc_nvenc -preset p4 -b:v 4M \
        -f mpegts /tmp/out.ts 2>/tmp/ff.log || { tail -20 /tmp/ff.log; exit 1; }
    tail -3 /tmp/ff.log
    SIZE=$(stat -c%s /tmp/out.ts)
    echo "output bytes: $SIZE"
    # Confirm hevc_nvenc was actually used (not a silent CPU fallback) and produced data.
    if grep -qi "hevc_nvenc" /tmp/ff.log && [ "$SIZE" -gt 10000 ]; then
        echo "V6-NVENC PASS: hevc_nvenc encoded headless :99 capture ($SIZE bytes)"
    else
        echo "V6-NVENC FAIL"; tail -20 /tmp/ff.log; exit 1
    fi
'
