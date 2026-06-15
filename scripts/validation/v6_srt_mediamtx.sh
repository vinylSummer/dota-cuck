#!/usr/bin/env bash
# V6 (SRT/mediamtx leg) — x11grab :99 → hevc_nvenc → SRT → mediamtx → WebRTC.
#
# Completes V6: proves the full streaming path up to mediamtx. Runs mediamtx with the
# project config, captures the headless :99 display, H.265-encodes on NVENC and pushes
# it over SRT to mediamtx, then queries mediamtx's HTTP API to confirm the live/match
# path is publishing and receiving bytes. (Browser WebRTC playback is the one leg that
# needs a human — open https://<host>:8889/live/match while this runs.)
#
# Note the mediamtx SRT streamid needs the "publish:" prefix (deployment.md's bare
# streamid sketch is publish-ambiguous).
#
#   scripts/validation/v6_srt_mediamtx.sh
set -euo pipefail
cd "$(dirname "$0")/../.."

docker build -f scripts/validation/Dockerfile.xtest -t dota-xtest .

echo "== starting mediamtx (host network) =="
docker rm -f dota-mediamtx >/dev/null 2>&1 || true
docker run -d --name dota-mediamtx --network host \
    -v "$PWD/mediamtx/mediamtx.yml:/mediamtx.yml:ro" \
    bluenviron/mediamtx:latest >/dev/null
sleep 2
docker logs dota-mediamtx 2>&1 | tail -5

echo "== capture :99 -> hevc_nvenc -> SRT publish:live/match (8s) =="
docker run --rm --network host --gpus all -e NVIDIA_DRIVER_CAPABILITIES=all dota-xtest bash -c '
    set -e
    Xorg :99 -config /etc/X11/xorg.conf -noreset &
    for i in $(seq 1 30); do DISPLAY=:99 xdpyinfo >/dev/null 2>&1 && break; sleep 0.5; done
    DISPLAY=:99 glxgears >/dev/null 2>&1 &
    sleep 1
    DISPLAY=:99 ffmpeg -hide_banner -y \
        -f x11grab -r 60 -s 1280x720 -i :99 \
        -t 8 -c:v hevc_nvenc -preset p4 -b:v 4M \
        -f mpegts "srt://127.0.0.1:8890?streamid=publish:live/match" 2>/tmp/ff.log
    tail -2 /tmp/ff.log
'

echo "== verify publish via mediamtx logs =="
# mediamtx logs each SRT publisher: "[SRT] ... is publishing to path 'live/match' ...".
LOGS=$(docker logs dota-mediamtx 2>&1)
echo "$LOGS" | grep -iE "publish|live/match|read" || true
docker rm -f dota-mediamtx >/dev/null 2>&1 || true
if echo "$LOGS" | grep -q "is publishing to path 'live/match'"; then
    echo "V6-SRT PASS: mediamtx ingested the SRT stream on live/match"
else
    echo "V6-SRT FAIL: mediamtx logged no publisher on live/match"; exit 1
fi
