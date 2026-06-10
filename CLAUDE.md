# Dota 2 Spectator-as-a-Service

A self-hosted service that allows users to spectate live Dota 2 matches played by their Steam
friends through a web interface. Users authenticate, link their Steam account, and can start a
live spectator stream for any friend currently in a Dota 2 match.

Single Debian server, RTX 3090. Architected from day one for clean extension to multiple workers
and a Kubernetes deployment in V2, but V1 targets a single worker and a single concurrent session.

Deployed at `dota.example.com` via nginx + certbot TLS.

---

## Architecture Overview

```
Browser
  │
  │ HTTPS (REST + WebSocket)              WebRTC
  │◄────────────────────────────────────────────────►│
  ▼                                                  │
nginx (TLS termination, dota.example.com:443)        │
  │                                                  │
  ├─── /api/*, /ws ──────────────────────────────────►Control Plane (Go)
  │                                                  │   │
  └─── /webrtc/* ────────────────────────────────────►mediamtx
                                                     │   ▲
                                            gRPC     │   │ SRT
                                       (bidir stream)│   │
                                                     ▼   │
                                              Worker (Python)
                                              Xorg + NVIDIA
                                              Steam (GUI)
                                              Dota 2
                                              FFmpeg
                                                 │
                                                 ▼
                                            PostgreSQL 18+
```

---

## Components

### Control Plane (Go)

**Responsibilities:**
- User authentication (JWT)
- Steam account management (add, remove, list)
- Friend status polling via Steam Web API
- Match session lifecycle management
- Worker pool management (V1: single worker)
- gRPC server — receives WorkerEvents, sends Commands via bidirectional stream
- HTTP API (Chi router) for frontend consumption
- WebSocket push for real-time events (Steam Guard prompts, session state changes, stream ready)

**Libraries:**
- `github.com/go-chi/chi` — HTTP router
- `google.golang.org/grpc` — gRPC server
- `github.com/jackc/pgx/v5` — PostgreSQL driver
- `nhooyr.io/websocket` — WebSocket
- `golang.org/x/crypto/argon2` — password hashing + credential key derivation

**Does NOT:**
- Run Steam or Dota
- Capture or encode video
- Communicate with Steam GC directly

---

### Worker (Python)

**Responsibilities:**
- Connect to control plane gRPC server on startup, open bidirectional stream
- Receive Commands, send Events
- Log into Steam (python-steam for GC queries; GUI Steam for Dota automation)
- Query Dota 2 Game Coordinator for live match ID for a given Steam ID
- Launch and automate Dota 2 on headless Xorg (DISPLAY=:99)
- Join match in spectator mode, select player-follow camera via console commands
- Run FFmpeg pipeline: x11grab → H.265 NVENC → SRT → mediamtx
- Persist sentry (Steam Guard device trust) file after first login
- Report all state transitions and errors to control plane

**Libraries:**
- `steam` (python-steam) + `dota2` (python-dota2) — GC communication
- `grpcio` + `grpcio-tools` — gRPC client
- `pyautogui` or `python-xlib` — GUI automation fallback if needed

---

### mediamtx

- Accepts SRT stream from FFmpeg on worker
- Outputs WebRTC to browser (built-in ICE/STUN/signaling)
- Runs as a dedicated Docker container
- V1: single SRT input path (`live/match`)
- Config at `mediamtx/mediamtx.yml`

---

### Frontend (React)

Barebones SPA. No design system. No CSS framework required. Functionality over appearance.

**Pages/views:**
- `/login` — username + password form
- `/` — friends list, online/in-match status, Spectate button per friend
- `/watch/:sessionId` — fullscreen WebRTC video player + disconnect button

**Components:**
- `SteamGuardModal` — overlay, appears when server pushes `steam_guard` WebSocket event, accepts code input, submits to `/api/sessions/:id/steamguard`

**Real-time:** single WebSocket connection maintained on authenticated pages, handles all push events.

---

### nginx

- TLS termination (certbot, `dota.example.com:443`)
- Proxy rules:
  - `/api/*` → control plane HTTP `:42000`
  - `/ws` → control plane WebSocket `:42001`
  - `/webrtc/*` → mediamtx `:42002`
- Serves static React build from `/usr/share/nginx/html`

---

## gRPC Contract

**File: `proto/worker.proto`**

The control plane is the gRPC **server**. Workers are gRPC **clients**. Each worker opens a single
long-lived `WorkerSession` bidirectional stream on startup. The control plane pushes `Command`
messages down this stream at any time; the worker pushes `WorkerEvent` messages up.

```protobuf
syntax = "proto3";
package spectator.v1;

option go_package = "github.com/youruser/dota-spectator/gen/spectator/v1";

// === Service ===

service ControlPlaneService {
  // Worker opens this on startup. Control plane pushes Commands; worker pushes Events.
  rpc WorkerSession(stream WorkerEvent) returns (stream Command);
}

// === Events: Worker → Control Plane ===

message WorkerEvent {
  string worker_id = 1;
  oneof payload {
    WorkerReady        ready             = 2;
    StatusUpdate       status_update     = 3;
    SteamGuardRequired steam_guard       = 4;
    MatchIdResolved    match_id_resolved = 5;
    StreamStarted      stream_started    = 6;
    ErrorEvent         error             = 7;
  }
}

message WorkerReady        {}
message StatusUpdate       { WorkerState state = 1; }
message SteamGuardRequired { SteamGuardType guard_type = 1; }
message MatchIdResolved    { uint64 match_id = 1; string steam_id = 2; }
message StreamStarted      { string srt_url = 1; }
message ErrorEvent         { string code = 1; string message = 2; bool fatal = 3; }

// === Commands: Control Plane → Worker ===

message Command {
  oneof payload {
    StartSpectate        start_spectate  = 1;
    StopSpectate         stop_spectate   = 2;
    SubmitSteamGuardCode steam_guard     = 3;
  }
}

message StartSpectate {
  string session_id    = 1;
  string target_steam_id = 2;       // friend's Steam ID to spectate
  string steam_username  = 3;       // credentials decrypted in memory by control plane
  string steam_password  = 4;
  bytes  sentry_hash     = 5;       // device trust token if available; empty on first login
}

message StopSpectate         {}
message SubmitSteamGuardCode { string code = 1; }

// === Enums ===

enum WorkerState {
  WORKER_STATE_UNSPECIFIED = 0;
  STOPPED    = 1;
  STARTING   = 2;
  IDLE       = 3;
  SPECTATING = 4;
  STOPPING   = 5;
}

enum SteamGuardType {
  STEAM_GUARD_TYPE_UNSPECIFIED = 0;
  EMAIL  = 1;
  MOBILE = 2;
}
```

---

## HTTP API (Control Plane — Chi)

```
POST   /api/auth/register                     { username, password }
POST   /api/auth/login                        { username, password }
POST   /api/auth/logout

GET    /api/steam/accounts                    list linked Steam accounts
POST   /api/steam/accounts                    { steam_username, steam_password }
DELETE /api/steam/accounts/:id

GET    /api/friends                           friends list with online + in-match status

POST   /api/sessions                          { target_steam_id } — start spectating
DELETE /api/sessions/:id                      stop spectating
GET    /api/sessions/:id                      session status + webrtc_url when ready

POST   /api/sessions/:id/steamguard           { code } — submit Steam Guard code

WS     /ws                                    push event stream
```

### WebSocket push events (server → client)

```json
{ "type": "session_state",  "session_id": "...", "state": "WATCHING" }
{ "type": "steam_guard",    "session_id": "...", "guard_type": "EMAIL" }
{ "type": "stream_ready",   "session_id": "...", "webrtc_url": "https://dota.example.com/webrtc/live/match" }
{ "type": "error",          "session_id": "...", "code": "DOTA_CRASH", "message": "..." }
```

---

## Database Schema

Migrations in `control-plane/db/migrations/` using sequential numbered SQL files.

```sql
-- Users
CREATE TABLE users (
  id            UUID        PRIMARY KEY DEFAULT gen_random_uuid_v7(),
  username      TEXT        UNIQUE NOT NULL,
  password_hash TEXT        NOT NULL,        -- Argon2id
  created_at    TIMESTAMPTZ DEFAULT now()
);

-- Steam accounts linked to users (one per user in V1)
CREATE TABLE steam_accounts (
  id             UUID  PRIMARY KEY DEFAULT gen_random_uuid_v7(),
  user_id        UUID  REFERENCES users(id) ON DELETE CASCADE,
  steam_id       TEXT  NOT NULL,
  steam_username TEXT  NOT NULL,
  enc_password   BYTEA NOT NULL,             -- AES-256-GCM ciphertext
  enc_nonce      BYTEA NOT NULL,             -- GCM nonce
  sentry_hash    BYTEA,                      -- Steam Guard device trust; stored after first login
  created_at     TIMESTAMPTZ DEFAULT now()
);

-- Workers (V1: one row, inserted at startup)
CREATE TABLE workers (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid_v7(),
  state      TEXT NOT NULL DEFAULT 'STOPPED',
  last_seen  TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Spectator sessions
CREATE TABLE sessions (
  id               UUID   PRIMARY KEY DEFAULT gen_random_uuid_v7(),
  user_id          UUID   REFERENCES users(id),
  worker_id        UUID   REFERENCES workers(id),
  steam_account_id UUID   REFERENCES steam_accounts(id),
  target_steam_id  TEXT   NOT NULL,          -- Steam ID of friend being spectated
  match_id         BIGINT,                   -- resolved from GC; null until known
  state            TEXT   NOT NULL DEFAULT 'OFF',
  webrtc_url       TEXT,
  started_at       TIMESTAMPTZ,
  ended_at         TIMESTAMPTZ,
  created_at       TIMESTAMPTZ DEFAULT now()
);
```

---

## Credential Security Model

Steam passwords are encrypted at rest. The encryption key is derived from the user's login
password and is **never stored**.

```
user_login_password
        │
        ▼ Argon2id (time=3, memory=64MB, threads=4, keyLen=32)
encryption_key (32 bytes)
        │
        ▼ AES-256-GCM
enc_password + enc_nonce  →  stored in DB
```

**At session start:**
1. User's plaintext password is present in the login request
2. Control plane re-derives the encryption key
3. Decrypts `enc_password` in memory
4. Passes plaintext credentials to worker via gRPC `StartSpectate` (internal Docker network only, no external exposure)
5. Worker uses credentials, does not persist them

**A database dump alone cannot decrypt Steam credentials.**

---

## Worker State Machine

```
         container start / gRPC stream connects
                        │
                        ▼
                      IDLE ◄──────────────────────────────┐
                        │                                  │
              StartSpectate received                       │
                        │                                  │
                        ▼                                  │
                    STARTING                               │
              (python-steam login,                         │
               GC query → match ID,                        │
               GUI Steam login,                            │
               Dota launch,                                │
               match join,                                 │
               FFmpeg start)                               │
                        │                                  │
                        ▼                                  │
                  SPECTATING                               │
                        │                                  │
              StopSpectate received                        │
              or fatal error                               │
                        │                                  │
                        ▼                                  │
                    STOPPING                               │
              (FFmpeg stop,                                │
               Dota close,                                 │
               Steam close)                                │
                        │                                  │
                        └──────────────────────────────────┘

Steam Guard interrupt (during STARTING):
  → send SteamGuardRequired event
  → pause login flow
  → wait for SubmitSteamGuardCode command
  → resume login flow
```

---

## Session State Machine (Control Plane)

```
OFF
 │  POST /api/sessions (worker allocated, StartSpectate sent)
 ▼
STARTING
 │  StreamStarted event received from worker
 ▼
WATCHING  (webrtc_url set, pushed to client via WebSocket)
 │  DELETE /api/sessions/:id  or  fatal ErrorEvent received
 ▼
STOPPING  (StopSpectate sent to worker)
 │  StatusUpdate IDLE received
 ▼
OFF
```

---

## Streaming Pipeline (V1)

```
Dota 2
(60 fps, 1280x720, headless Xorg :99, NVIDIA GPU)
        │
        ▼
FFmpeg x11grab
        │
        ▼
H.265 NVENC (hevc_nvenc)
720p, 60fps, ~4Mbps
        │
        ▼
SRT → mediamtx container (localhost:8890)
        │
        ▼
mediamtx WebRTC output
        │
        ▼
Browser (fullscreen video element)
```

**FFmpeg command sketch (worker):**
```bash
ffmpeg \
  -f x11grab -r 60 -s 1280x720 -i :99 \
  -c:v hevc_nvenc -preset p4 -b:v 4M \
  -f mpegts "srt://mediamtx:8890?streamid=live/match"
```

**Dota launch options:** `-novid -console -nosound` (adjust as needed for headless)

**V2 addition:** nvinterpolate FFmpeg filter for 30fps render → 60fps stream. Do not implement in V1.

---

## Docker Compose Service Map

```
docker-compose.yml
├── postgres
│     image: postgres:18
│     volume: pgdata
│
├── control-plane
│     build: ./control-plane
│     depends_on: postgres
│     ports
│     environment: DATABASE_URL, JWT_SECRET, STEAM_API_KEY, GRPC_LISTEN_ADDR
│
├── worker
│     build: ./worker
│     depends_on: control-plane, mediamtx
│     environment: CONTROL_PLANE_ADDR, DISPLAY=:99
│     volumes: steam-data (Dota install, Steam userdata, sentry files)
│     deploy:
│       resources:
│         reservations:
│           devices:
│             - driver: nvidia
│               capabilities: [gpu, compute, video]
│
├── mediamtx
│     image: bluenviron/mediamtx:latest
│     config: ./mediamtx/mediamtx.yml
│     ports: 
│
nginx not dockerized, running on host on 443
```

Worker container requires a custom `xorg.conf` specifying the RTX 3090 BusID for headless
GPU rendering. The BusID must match the host PCI address (find with `nvidia-smi` or
`lspci | grep NVIDIA`). Template at `worker/xorg/xorg.conf`.

---

## Repository Structure

```
/
├── proto/
│   └── worker.proto
│
├── control-plane/
│   ├── cmd/server/main.go
│   ├── internal/
│   │   ├── auth/            JWT, Argon2id hashing, credential encryption
│   │   ├── steam/           Steam Web API friend/status polling
│   │   ├── sessions/        Session state machine, lifecycle management
│   │   ├── workers/         Worker pool (V1: single worker), gRPC stream handler
│   │   └── api/             HTTP handlers (Chi), WebSocket hub
│   ├── db/
│   │   └── migrations/      001_init.sql, etc.
│   ├── gen/                 Generated protobuf Go code (do not edit)
│   └── Dockerfile
│
├── worker/
│   ├── agent.py             Main entry point, state machine
│   ├── grpc_client.py       Bidirectional stream, event/command dispatch
│   ├── steam_client.py      python-steam login, GC match ID query, sentry handling
│   ├── dota_client.py       GUI Dota automation (launch, join match, camera)
│   ├── ffmpeg.py            FFmpeg subprocess management
│   ├── xorg/
│   │   └── xorg.conf        Headless NVIDIA display config (fill in BusID)
│   ├── gen/                 Generated protobuf Python code (do not edit)
│   ├── requirements.txt
│   └── Dockerfile
│
├── frontend/
│   ├── src/
│   │   ├── App.jsx
│   │   ├── api.js           Fetch wrappers for all HTTP endpoints
│   │   ├── ws.js            WebSocket singleton
│   │   ├── pages/
│   │   │   ├── Login.jsx
│   │   │   ├── Friends.jsx
│   │   │   └── Watch.jsx
│   │   └── components/
│   │       └── SteamGuardModal.jsx
│   ├── package.json
│   └── Dockerfile
│
├── mediamtx/
│   └── mediamtx.yml
│
├── nginx/
│   └── nginx.conf
│
├── docker-compose.yml
└── CLAUDE.md
```

---

## Known Risks — Validate Manually Before Implementing

These must be confirmed on the actual server before writing the worker agent.

### ⚠️ CRITICAL: Dual Steam session problem

python-steam (GC queries) and the GUI Steam client cannot both be logged into the same account
simultaneously — Steam will terminate one session. The intended handoff:

```
python-steam login
    → GC query (get match ID for target_steam_id)
    → python-steam logout
    → GUI Steam launch + login
    → Dota launch
    → spectate match_id
```

**Risk:** GUI Steam login may re-trigger Steam Guard even after python-steam already established
device trust (they use separate sentry files). Validate this flow manually. If both need separate
Steam Guard confirmations, the two-prompt UX must be explicitly handled in the session state
machine and surfaced to the user.

**Mitigation to test:** pre-populate the GUI Steam sentry file from the python-steam session, or
accept that first login requires two Steam Guard confirmations and subsequent logins use saved
sentry for both.

### ⚠️ Dota spectate console command

The exact console command sequence to join a live match by match ID must be confirmed. Candidate:

```
dota_spectate_game <match_id>
```

or potentially requires a lobby/server connect flow. **Validate headlessly with a known live
match ID before implementing the automation layer.**

### ⚠️ Headless Xorg inside Docker with NVIDIA

Xorg must run on the GPU without a physical display. Requires:
- `xorg.conf` with correct `BusID` for the RTX 3090
- `nvidia-container-toolkit` on host
- `DISPLAY=:99` set in container

**Validate that a GPU-accelerated process (e.g. `glxinfo`, `nvidia-smi`) works inside the worker
container before writing any automation code.**

### ⚠️ Steam + Dota installation in Docker

Dota 2 is ~70GB. Recommended approach: install via `steamcmd` during image build into a named
Docker volume so it survives container rebuilds. A strategy to handle updates should be devised. Define the overall strategy before writing the
Dockerfile — it has large build time implications.

---

## V1 Scope

**In V1:**
- User registration and login
- One linked Steam account per user
- Steam friends list with in-match status
- Start / stop spectating (single worker, single concurrent session)
- Steam Guard interactive flow (modal in frontend)
- WebRTC stream in browser (fullscreen)
- Error display

**Explicitly deferred to V2:**
- WARM state (keep Steam/Dota alive between sessions)
- Multiple concurrent sessions and worker pool
- Frame interpolation (nvinterpolate ffmpeg filter)
- Match session sharing (multiple viewers, one Dota instance)
- Kubernetes deployment
- PWA / mobile app
- AI-assisted crash recovery and UI adaptation

---

## Implementation Order

1. `proto/worker.proto` — finalise and generate Go + Python code before any other code
2. `db/migrations/` — schema only, no application code yet
3. Control plane skeleton — gRPC server accepts connections, HTTP router returns 501s, WebSocket hub compiles
4. Worker skeleton — gRPC client connects, state machine logs transitions, no Steam yet
5. Auth — register/login, Argon2id, JWT, AES-256-GCM credential storage
6. Steam friend polling — Steam Web API integration, `/api/friends` endpoint
7. Worker Steam/GC — python-steam login, GC match ID query, sentry persistence
8. Worker Dota automation — headless launch, spectate command, camera follow
9. Worker FFmpeg pipeline — x11grab → hevc_nvenc → SRT → mediamtx
10. Frontend — Login, Friends, Watch pages, SteamGuardModal, WebSocket integration
11. Docker Compose — wire all services, GPU config, volume strategy
12. nginx + TLS — final external deployment
