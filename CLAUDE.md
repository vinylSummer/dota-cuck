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
- Steam account management (add, remove, list); encrypts credentials at link time
- Friend list + in-match status — requested from the worker's authenticated Steam
  session over gRPC, **not** the public Steam Web API (see Friends Data Source below)
- In-memory credential-key cache (derived at login; used to encrypt/decrypt Steam
  credentials without re-prompting)
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
- Log into Steam (python-steam for GC queries + friends listing; GUI Steam for Dota
  automation). Set `set_credential_location` so sentries persist; capture the
  `login_key` (`new_login_key` event) for password-less `relogin()` on later logins
- Keep one **warm** headless python-steam session (lazily connected on the first
  `ListFriends`, kept alive via `run_forever`/relogin) so friends are served
  instantly. List the logged-in account's friends with online + in-match status
  (`ListFriends` command → `FriendsResult` event); report its own `steam_id` so the
  control plane can backfill `steam_accounts.steam_id`. The headless session is
  **dropped while spectating** (GUI Steam needs the account — dual-session) and
  re-warms lazily afterwards
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

### Friends Data Source (why not the Steam Web API)

The friends list and in-match status come from the **worker's authenticated python-steam
session**, not the Steam Web API. The Web API key is a developer key with no per-user
authorization: `GetFriendList` returns `401` for private friend lists, and `GetPlayerSummaries`
hides friends-only game presence — so it cannot reliably report whether a friend is in a Dota
match (the core signal). Verified live (2026-06-12): valid key, public profile, friend list still
`401`. An authenticated session sees exactly what the user sees, regardless of privacy.

Flow: `GET /api/friends` → control plane decrypts credentials (in-memory key) → `ListFriends`
command over the worker gRPC stream → worker connects its warm headless session if not already
connected (relogin via `login_key` when available), lists friends, replies `FriendsResult`
correlated by `request_id` → control plane maps to the DTO. The control plane always **pulls** on
request; the worker keeps the session **warm** between calls so the reply is fast. Live presence
*push* to the browser is V2.

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
    FriendsResult      friends_result    = 8;
  }
}

message WorkerReady        {}
message StatusUpdate       { WorkerState state = 1; }
message SteamGuardRequired { SteamGuardType guard_type = 1; }
message MatchIdResolved    { uint64 match_id = 1; string steam_id = 2; }
message StreamStarted      { string srt_url = 1; }
message ErrorEvent         { string code = 1; string message = 2; bool fatal = 3; }

// Response to a ListFriends command. Correlated by request_id. On failure,
// `error` is set and `friends` is empty. `owner_steam_id` is the logged-in
// account's own Steam ID, used to backfill steam_accounts.steam_id.
message FriendsResult {
  string     request_id     = 1;
  repeated Friend friends    = 2;
  string     owner_steam_id  = 3;
  ErrorEvent error           = 4;
}

message Friend {
  string steam_id     = 1;
  string persona_name = 2;
  bool   online       = 3;
  bool   in_match     = 4;   // currently in a Dota 2 game
}

// === Commands: Control Plane → Worker ===

message Command {
  oneof payload {
    StartSpectate        start_spectate  = 1;
    StopSpectate         stop_spectate   = 2;
    SubmitSteamGuardCode steam_guard     = 3;
    ListFriends          list_friends    = 4;
  }
}

message StartSpectate {
  string session_id    = 1;
  string target_steam_id = 2;       // friend's Steam ID to spectate
  string steam_username  = 3;       // credentials decrypted in memory by control plane
  string steam_password  = 4;
  bytes  sentry_hash     = 5;       // device trust token if available; empty on first login
}

// Friends fetch. The worker serves this from its warm headless session,
// connecting lazily (relogin via login_key when available, else credentials)
// on the first call, then replying with FriendsResult.
message ListFriends {
  string request_id     = 1;        // correlates the FriendsResult reply
  string steam_username  = 2;       // decrypted in memory by control plane
  string steam_password  = 3;
  bytes  sentry_hash     = 4;       // device trust token if available
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
                                              (served from the worker's authenticated Steam
                                              session, not the Web API; 409 if no Steam
                                              account linked, 502 on Steam/worker failure)

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

### OpenAPI / Swagger documentation

The HTTP API is documented with **swaggo** (code-first). Each handler in
`control-plane/internal/api/handlers.go` carries `// @...` annotations; request
and response shapes are the DTO structs in `internal/api/models.go`. General
API info lives above `main()` in `cmd/server/main.go`.

- Regenerate after changing annotations or DTOs: `make docs` (runs `swag init`
  → `control-plane/docs/`). The generated `docs/` package is **committed** so
  the binary builds without the swag CLI; `main.go` blank-imports it.
- Served at **`/docs`** (Swagger UI); raw spec at `/docs/doc.json`.
- swaggo emits Swagger 2.0; the spec's `basePath` is `/api`.

---

## Database Schema

Migrations in `control-plane/db/migrations/` using sequential numbered SQL files.

```sql
-- Users
CREATE TABLE users (
  id            UUID        PRIMARY KEY DEFAULT uuidv7(),
  username      TEXT        UNIQUE NOT NULL,
  password_hash TEXT        NOT NULL,        -- Argon2id
  kdf_salt      BYTEA       NOT NULL,        -- per-user salt for credential key derivation
  created_at    TIMESTAMPTZ DEFAULT now()
);

-- Steam accounts linked to users (one per user in V1)
CREATE TABLE steam_accounts (
  id             UUID  PRIMARY KEY DEFAULT uuidv7(),
  user_id        UUID  REFERENCES users(id) ON DELETE CASCADE,
  steam_id       TEXT,                       -- backfilled from worker's first login; null until then
  steam_username TEXT  NOT NULL,
  enc_password   BYTEA NOT NULL,             -- AES-256-GCM ciphertext
  enc_nonce      BYTEA NOT NULL,             -- GCM nonce
  sentry_hash    BYTEA,                      -- Steam Guard device trust; stored after first login
  created_at     TIMESTAMPTZ DEFAULT now()
);

-- Workers (V1: one row, inserted at startup)
CREATE TABLE workers (
  id         UUID PRIMARY KEY DEFAULT uuidv7(),
  state      TEXT NOT NULL DEFAULT 'STOPPED',
  last_seen  TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Spectator sessions
CREATE TABLE sessions (
  id               UUID   PRIMARY KEY DEFAULT uuidv7(),
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
password and is **never written to disk**.

```
user_login_password
        │
        ▼ Argon2id (time=3, memory=64MB, threads=4, keyLen=32, salt=users.kdf_salt)
encryption_key (32 bytes)
        │
        ▼ AES-256-GCM
enc_password + enc_nonce  →  stored in DB
```

**In-memory key cache.** The login password is only present at `POST /api/auth/login`. Other
actions (linking a Steam account, listing friends, starting a spectate) are JWT-authed and do
not carry it. So at login the control plane derives the key once and holds it in a server-side
**in-memory cache** keyed by user, evicted on logout or token expiry. The cache is the only thing
that can decrypt `enc_password`, and it never leaves RAM.

**Account link (`POST /api/steam/accounts`):**
1. Control plane takes the cached key for the user
2. Encrypts the Steam password → `enc_password` + `enc_nonce`, stored in DB (`steam_id` left null)

**Friends / session start:**
1. Control plane takes the cached key, decrypts `enc_password` in memory
2. Passes plaintext credentials to the worker via gRPC (`ListFriends` / `StartSpectate`) — internal
   Docker network only, no external exposure
3. Worker uses credentials, does not persist them (it may persist the sentry file and `login_key`
   for password-less `relogin()`); reports its own `steam_id` to backfill the row

**A database dump alone cannot decrypt Steam credentials** (the key is never persisted). A memory
dump of the running control plane could expose cached keys — an accepted V1 tradeoff.

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
│   ├── docs/                Generated OpenAPI spec (swag init; do not edit)
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
- Steam friends list with in-match status, served from a **warm headless python-steam
  session** (kept alive between friends calls; dropped during spectate)
- Start / stop spectating (single worker, single concurrent session)
- Steam Guard interactive flow (modal in frontend)
- WebRTC stream in browser (fullscreen)
- Error display

**Explicitly deferred to V2:**
- WARM state for **Dota/GUI Steam** (keep the game alive between spectate sessions; the
  headless friends session warmth above is V1)
- Live presence *push* to the browser (V1 pulls on request)
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
6. Friends — in-memory credential-key cache, account linking (`POST /api/steam/accounts`),
   `ListFriends`/`FriendsResult` proto, worker-backed `/api/friends`, and the worker's
   python-steam friend listing. (The Steam Web API client built earlier is retired as the
   friends source — see "Friends Data Source".) Overlaps the worker Steam login of step 7.
7. Worker Steam/GC — python-steam login (shared with friends), GC match ID query, sentry +
   `login_key` persistence
8. Worker Dota automation — headless launch, spectate command, camera follow
8. Worker Dota automation — headless launch, spectate command, camera follow
9. Worker FFmpeg pipeline — x11grab → hevc_nvenc → SRT → mediamtx
10. Frontend — Login, Friends, Watch pages, SteamGuardModal, WebSocket integration
11. Docker Compose — wire all services, GPU config, volume strategy
12. nginx + TLS — final external deployment

---

## Testing

**Philosophy:** test code that makes decisions; skip glue and stubs. A handler that returns
`501`, or a Steam/Dota/FFmpeg call with no logic yet, has nothing to assert — adding tests there
tests the framework, not us. Introduce tests for each piece *when that piece gains real behaviour*,
not before. State machines, request routing, serialization contracts, and crypto are the things
worth covering.

`make test` runs `go test ./...` (control plane) and `pytest` (worker).

**Database-backed tests run against real PostgreSQL, not a mock.** Anything that
touches the DB (the `store` package and the auth HTTP handlers) requires a
running instance at `POSTGRESQL_URL`; without it those tests **fail loudly**,
they do not skip. `make test-go` wraps the run in `scripts/with-test-db.sh`,
which spins up an ephemeral cluster (`initdb` + `pg_ctl`, unix-socket only, torn
down after) and sets `POSTGRESQL_URL` — so contributors don't need to configure
anything, and the code is exercised against the same engine and migrations as
production. `internal/testdb` gives each test a fresh throwaway database with all
migrations applied. Set `POSTGRESQL_URL` yourself to run against an existing
instance; set `PG_BINDIR` if `initdb`/`pg_ctl` aren't on `PATH` (e.g. Debian).

**Design constraint:** the session and worker state machines must be **pure transition functions /
tables** (e.g. `Next(cur, event) (next, error)`), not logic buried inside handlers. This is the
right shape regardless and is what makes them testable.

### Tests for the steps 2–4 skeleton

Control plane (Go, stdlib `testing`, table-driven):
- **Session state machine** (`internal/sessions`): every valid edge advances
  (`OFF→STARTING→WATCHING→STOPPING→OFF`); invalid edges error; a fatal-error event from any active
  state routes to `STOPPING`.
- **HTTP router contract** (`internal/api`, `httptest`): every documented route is registered and
  returns `501`; unknown paths return `404`. Locks the API surface.
- **WebSocket push-event marshaling** (`internal/api`): the four events (`session_state`,
  `steam_guard`, `stream_ready`, `error`) marshal to exactly the JSON shape in the spec — the
  frontend depends on these field names.
- **gRPC `WorkerSession` handler** (`internal/workers`): in-memory bidi stream — worker connects,
  sends `WorkerReady`, is registered and state updates; a pushed `Command` reaches the stream.

Worker (Python, `pytest`):
- **Worker state machine**: parametrized valid/invalid transitions
  (`STOPPED→STARTING→IDLE→SPECTATING→STOPPING`).
- **Command dispatch** (`grpc_client.py`): each `Command` oneof variant routes to the correct
  (mocked) handler.

Auth/crypto tests land with **step 5** — Argon2id and AES-256-GCM are the most important tests in
the project. Migrations are exercised transitively: `internal/testdb` applies every
`db/migrations/*.sql` against the throwaway database before each DB-backed test runs.

---

## Commit Discipline

Prefer small, single-purpose commits, each independently revertable. Don't bundle unrelated
changes into one commit. Typical granularity for the skeleton milestone: migrations as one commit;
control-plane skeleton as another; worker skeleton as another; tests committed alongside the code
they cover (or as a focused follow-up). A reader should be able to `git revert` any single commit
without untangling others.
