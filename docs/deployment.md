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

Config at `nginx/nginx.conf` (a site block; certbot fills in the cert paths).

- TLS termination (certbot, `dota.example.com:443`)
- Proxy rules:
  - `/api/*` → control plane HTTP `127.0.0.1:42000`
  - `/ws` → control plane WebSocket `127.0.0.1:42000` (same server, Upgrade headers)
  - `/webrtc/*` → mediamtx WHEP `127.0.0.1:8889` (the `/webrtc/` prefix is stripped,
    mapping `/webrtc/live/match` → mediamtx `/live/match`)
- Serves the static React build (`cd frontend && npm run build`) from `/usr/share/nginx/html`

## Docker Compose service map

```
docker-compose.yml
├── postgres        image postgres:18; volume pgdata; healthcheck (pg_isready)
├── control-plane   build ./control-plane (distroless Go binary); depends_on postgres healthy
│                   env: DATABASE_URL (from POSTGRES_*), JWT_SECRET, CREDENTIAL_PEPPER,
│                        PUBLIC_BASE_URL, HTTP/GRPC_LISTEN_ADDR
│                   applies db/migrations on boot (idempotent; store.Migrate)
│                   ports 127.0.0.1:42000 (HTTP/WS) + 42010 (gRPC), nginx proxies in
├── mediamtx        image bluenviron/mediamtx:latest; config ./mediamtx/mediamtx.yml
│                   ports 127.0.0.1:8889 (WHEP) + 8890/udp (SRT ingest)
└── worker          build worker/Dockerfile; depends_on control-plane, mediamtx
                    env: CONTROL_PLANE_ADDR, WORKER_DOTA_BRINGUP=1, DISPLAY=:99,
                         MEDIAMTX_SRT_HOST/PORT, NVIDIA_DRIVER_CAPABILITIES=all
                    volume /fard/steam (Dota install + GUI Steam login state)
                    --gpus (deploy.resources nvidia [gpu,compute,video,graphics,utility]),
                    --device /dev/uinput, --shm-size 2g
nginx runs on the host on 443 (not in compose; nginx/nginx.conf).
```

**Worker image.** `worker/Dockerfile` builds **on the validated headless stack**
(`dota-steam`, from `scripts/validation/Dockerfile.{xtest,steam}` — build those first), adding the
uv worker project and a supervisord (`worker/deploy/supervisord.conf`) that brings up the desktop
(udev → dbus → Xorg :99 → Xfce, + optional VNC for the one-time Steam login) and runs the worker
agent. ⚠ **Needs live GPU validation**: the uinput device-enumeration ordering (devices must exist
before Xorg binds them via libinput) is proven in the harness via standalone daemons + an Xorg
restart; wiring it to the worker's in-process `DotaClient` devices must be validated on the server.

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
