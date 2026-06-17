# Deployment, Streaming & Infra

## Streaming Pipeline (V1)

```
Dota 2 (60 fps, 1280x720, headless Xorg :99, NVIDIA GPU)
   → FFmpeg x11grab
   → H.265 NVENC (hevc_nvenc), 720p 60fps ~4Mbps
   → SRT → mediamtx container (localhost:8890)
   → mediamtx WebRTC output
   → Browser (fullscreen video element)
```

**FFmpeg command sketch (worker):**
```bash
ffmpeg \
  -f x11grab -r 60 -s 1280x720 -i :99 \
  -c:v hevc_nvenc -preset p4 -b:v 4M \
  -f mpegts "srt://mediamtx:8890?streamid=publish:live/match"
```

The SRT `streamid` **must** be `publish:live/match` (the bare `live/match` is publish-ambiguous
and mediamtx rejects it) — confirmed in V6 (see [validation-results.md](validation-results.md)).

**Dota launch options:** `-novid -console -nosound` (adjust as needed for headless).

**V2 addition:** nvinterpolate FFmpeg filter for 30fps render → 60fps stream. Do not
implement in V1.

## mediamtx

Accepts the SRT stream from the worker's FFmpeg and outputs WebRTC to the browser
(built-in ICE/STUN/signaling). Dedicated Docker container; config at
`mediamtx/mediamtx.yml`. V1: single SRT input path (`live/match`).

## nginx (host, not dockerized)

- TLS termination (certbot, `dota.example.com:443`)
- Proxy rules:
  - `/api/*` → control plane HTTP `:42000`
  - `/ws` → control plane WebSocket `:42001`
  - `/webrtc/*` → mediamtx `:42002`
- Serves the static React build from `/usr/share/nginx/html`

## Docker Compose service map

```
docker-compose.yml
├── postgres        image postgres:18; volume pgdata
├── control-plane   build ./control-plane; depends_on postgres
│                   env: DATABASE_URL, JWT_SECRET, CREDENTIAL_PEPPER, GRPC_LISTEN_ADDR
├── worker          build ./worker; depends_on control-plane, mediamtx
│                   env: CONTROL_PLANE_ADDR, DISPLAY=:99
│                   volumes: steam-data (Dota install, Steam userdata, sentry files)
│                   deploy.resources.reservations.devices: nvidia [gpu, compute, video]
└── mediamtx        image bluenviron/mediamtx:latest; config ./mediamtx/mediamtx.yml
nginx runs on the host on 443 (not in compose).
```

The worker container needs a custom `xorg.conf` with the RTX 3090 `BusID` for headless
GPU rendering. The BusID must match the host PCI address (`nvidia-smi` or
`lspci | grep NVIDIA`). Template at `worker/xorg/xorg.conf`.

## Steam + Dota install in Docker

Dota 2 is ~70GB (≈72 GB logical; ~44 GB on the ZFS dataset with compression — V2). Install via
`steamcmd` **once into the bound `steam-data` volume at runtime, never at image build** — Docker
volumes aren't mounted during `build`, and V2 confirmed the install must target the persistent
dataset so it survives container rebuilds and host reboots. On this server the volume binds to
**`/fard/steam`** (the root fs is 98% full); `steamcmd +force_install_dir /fard/steam/dota …
+app_update 570 validate` is resumable. See V2 in [validation-results.md](validation-results.md).
The worker image must run with `--gpus all` + `NVIDIA_DRIVER_CAPABILITIES=all` and must **not**
install an NVIDIA driver package (the Container Toolkit injects the driver/GLX/NVENC libs — V1).
