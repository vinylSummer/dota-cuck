# Dota 2 Spectator-as-a-Service

A self-hosted service that lets users spectate live Dota 2 matches played by their Steam
friends through a web interface. Users authenticate, link their Steam account, and start a
live spectator stream for any friend currently in a Dota 2 match.

Single Debian server, RTX 3090. Architected for clean extension to multiple workers and a
Kubernetes deployment in V2, but V1 targets a single worker and a single concurrent session.
Deployed at `dota.example.com` via nginx + certbot TLS.

Detailed reference lives in `docs/`:
[grpc-contract](docs/grpc-contract.md) ·
[database](docs/database.md) ·
[worker](docs/worker.md) ·
[deployment](docs/deployment.md) ·
[known-risks](docs/known-risks.md) ·
[validation-results](docs/validation-results.md) ·
[testing](docs/testing.md)

---

## Architecture Overview

```
Browser
  │ HTTPS (REST + WebSocket)              WebRTC
  │◄────────────────────────────────────────────────►│
  ▼                                                  │
nginx (TLS termination, dota.example.com:443)        │
  │                                                  │
  ├─── /api/*, /ws ──────────────────────────────────►Control Plane (Go)
  │                                                  │   │
  └─── /webrtc/* ────────────────────────────────────►mediamtx
                                            gRPC     │   ▲ SRT
                                       (bidir stream)▼   │
                                              Worker (Python)
                                              Xorg + NVIDIA · Steam (GUI) · Dota 2 · FFmpeg
                                                 │
                                                 ▼
                                            PostgreSQL 18+
```

---

## Components

### Control Plane (Go)

- User authentication (JWT)
- Steam account management (add, remove, list); acquires + encrypts a Steam refresh token at link time
- Friend list + in-match status — requested from the worker's authenticated Steam session over
  gRPC, **not** the public Steam Web API (see Friends Data Source)
- In-memory credential-key cache (derived at login; encrypts/decrypts Steam credentials without
  re-prompting)
- Match session lifecycle; worker pool (V1: single worker)
- gRPC server (receives WorkerEvents, sends Commands over a bidirectional stream)
- HTTP API (Chi router) + WebSocket push (Steam Guard prompts, session state, stream ready)

**Does NOT** run Steam/Dota, capture or encode video, or talk to the Steam GC directly.

**Libraries:** `go-chi/chi` (HTTP), `google.golang.org/grpc`, `jackc/pgx/v5` (Postgres),
`nhooyr.io/websocket`, `golang.org/x/crypto/argon2` (password hashing + key derivation).

### Worker (Python)

Single process (Python 3.10, protobuf-3.20 line, uv-managed) — it logs into Steam, resolves the
target's live match, automates Dota on headless Xorg, and runs the FFmpeg pipeline. Keeps one
**warm in-process python-steam session** for friends, dropped while spectating. Full detail,
including the protobuf/uv rationale and module map, in [docs/worker.md](docs/worker.md).

**Match-ID resolution (validated 2026-06-15, not the GC):** the live match to spectate comes from
the target friend's **Steam rich presence** key `WatchableGameID` (present and >0 only for live,
watchable public matches), read from the **same warm python-steam session** that serves friends —
`request_persona_state(ids, state_flags=… | 0x200)` then read `rich_presence['WatchableGameID']`.
The Dota Game Coordinator (python-dota2) did **not** connect for a session not actively running
the game, so the earlier "query the GC for the match ID" plan is dropped: **no `dota2` dependency
is needed.** See [docs/validation-results.md](docs/validation-results.md) (V3).

### Friends Data Source (why not the Steam Web API)

Friends and in-match status come from the **worker's authenticated python-steam session**, not
the Steam Web API. The Web API key is a developer key with no per-user authorization:
`GetFriendList` returns `401` for private friend lists and `GetPlayerSummaries` hides
friends-only game presence — so it can't reliably report whether a friend is in a Dota match
(the core signal). Verified live (2026-06-12): valid key, public profile, friend list still
`401`. An authenticated session sees exactly what the user sees, regardless of privacy.

Flow: `GET /api/friends` → control plane decrypts the refresh token (in-memory key) → `ListFriends`
command over the worker stream → worker logs its warm session onto the CM with the refresh token
if needed, lists friends, replies `FriendsResult` correlated by `request_id`
→ control plane maps to the DTO. The control plane always **pulls** on request; the worker keeps
the session **warm** between calls. Live presence *push* to the browser is V2.

### mediamtx · Frontend · nginx

- **mediamtx** — Docker container; SRT in from the worker, WebRTC out to the browser. See
  [docs/deployment.md](docs/deployment.md).
- **Frontend** — barebones Vite + React SPA (react-router-dom), functionality over appearance.
  Pages: `/login` (login/register), `/` (friends list + Spectate button, falls back to the
  account-link form on a `409`), `/watch/:sessionId` (fullscreen WebRTC via WHEP + disconnect).
  `SteamGuardModal` appears on a `steam_guard` WS event and submits to the account
  (`/api/steam/accounts/:id/steamguard`) or session (`/api/sessions/:id/steamguard`) endpoint by
  event scope. `App` owns auth state, the single WebSocket, and the global guard modal. Decision
  logic (`ws.routeEvent`, `api` request contract, `auth.isAuthed`, `status.canSpectate`) is
  extracted into pure functions and unit-tested (Vitest + MSW); see [docs/testing.md](docs/testing.md).
- **nginx** — host TLS termination + proxy to control plane / mediamtx. See
  [docs/deployment.md](docs/deployment.md).

---

## gRPC Contract

Source of truth: `proto/spectator/v1/worker.proto`. The control plane is the gRPC **server**;
workers are **clients**, each opening one long-lived `WorkerSession` bidirectional stream.
Control plane pushes `Command`s; worker pushes `WorkerEvent`s; request/response pairs (e.g.
ListFriends → FriendsResult) correlate by `request_id`. Full message definitions and the swaggo
setup are in [docs/grpc-contract.md](docs/grpc-contract.md). Regenerate stubs with `make proto`.

---

## HTTP API (Control Plane — Chi)

```
POST   /api/auth/register            { username, password }
POST   /api/auth/login               { username, password }
POST   /api/auth/logout

GET    /api/steam/accounts           list linked Steam accounts
POST   /api/steam/accounts           { steam_username?, steam_password? } — empty body starts a
                                     QR link, creds start the email/no-2FA link; kicks off an async
                                     worker handshake that acquires + persists the encrypted refresh
                                     token and backfills steam_id (progress pushed over WS)
DELETE /api/steam/accounts/:id
POST   /api/steam/accounts/:id/steamguard  { code } — submit a Steam Guard code for an
                                     in-progress account link (404 unknown account,
                                     409 no prompt in progress)

GET    /api/friends                  friends list with online + in-match status (served from the
                                     worker's Steam session; 409 if no Steam account linked,
                                     502 on Steam/worker failure)

POST   /api/sessions                 { target_steam_id } — start spectating
DELETE /api/sessions/:id             stop spectating
GET    /api/sessions/:id             session status + webrtc_url when ready
POST   /api/sessions/:id/steamguard  { code } — submit Steam Guard code

WS     /ws                           push event stream
```

**WebSocket push events (server → client)** — exact JSON shape; the frontend depends on these names:
```json
{ "type": "session_state",  "session_id": "...", "state": "WATCHING" }
{ "type": "steam_guard",    "session_id": "...", "guard_type": "EMAIL" }
{ "type": "steam_guard",    "account_id": "...", "guard_type": "EMAIL" }
{ "type": "account_linked", "account_id": "...", "steam_id": "76561198..." }
{ "type": "stream_ready",   "session_id": "...", "webrtc_url": "https://dota.example.com/webrtc/live/match" }
{ "type": "error",          "session_id": "...", "code": "DOTA_CRASH",  "message": "..." }
{ "type": "error",          "account_id": "...", "code": "LINK_FAILED", "message": "..." }
```
Account-link events carry `account_id` (the link is not a session); spectate events carry
`session_id`. The QR/credentials handshake at account link yields a refresh token, so post-link
friends/spectate logins reuse it and don't re-prompt until it expires.

Documented with swaggo (`make docs`), served at `/docs`. See [docs/grpc-contract.md](docs/grpc-contract.md).

---

## Database Schema

PostgreSQL 18+. Tables: `users`, `steam_accounts` (one per user in V1), `workers`, `sessions`.
Migrations in `control-plane/db/migrations/` are the source of truth. Full DDL in
[docs/database.md](docs/database.md).

---

## Credential Security Model

Steam auth uses the modern **refresh-token** model. Account link runs the
`IAuthenticationService` handshake once (QR scan, or credentials for email-only/no-2FA accounts)
to obtain a long-lived **refresh token**; that token is encrypted at rest, and the encryption key
is derived from the user's login password and is **never written to disk**. The Steam **password
is never persisted** — on the credentials path it is used only for the handshake, then discarded.
There is no sentry and no `login_key`.

```
user_login_password
  ─Argon2id(time=3, memory=64MB, threads=4, keyLen=32, salt=users.kdf_salt)→ key (32B)
  ─AES-256-GCM→ enc_refresh_token + enc_refresh_nonce  (stored in DB)
```

**In-memory key cache.** The login password is only present at `POST /api/auth/login`; other
actions are JWT-authed and don't carry it. At login the control plane derives the key once and
holds it in a server-side **in-memory cache** keyed by user, evicted on logout or token expiry.
It's the only thing that can decrypt `enc_refresh_token`, and never leaves RAM.

- **Account link** (`POST /api/steam/accounts`): no body ⇒ QR link (the worker pushes the
  challenge URL over the WebSocket to render); `steam_username` + `steam_password` ⇒ credentials
  link (worker RSA-encrypts the password to Steam, drives an emailed code if required). On
  success the worker returns the **refresh token** + `steam_id`; the control plane encrypts the
  token with the cached key → `enc_refresh_token` + `enc_refresh_nonce` and backfills the row.
- **Friends / session start**: take the cached key, decrypt the refresh token in memory, pass it
  to the worker via gRPC (internal Docker network only). The worker logs onto the CM with the
  token (zero Steam Guard) and does not store it. On token expiry/revocation the user re-links.

A DB dump alone cannot authenticate (the key is never persisted, so `enc_refresh_token` can't be
decrypted; the password was never stored). A memory dump of the running control plane could
expose cached keys — an accepted V1 tradeoff.

---

## State Machines

Both are **pure transition functions** (`Next(cur, event) → (next, error)`), not logic in
handlers — see [docs/testing.md](docs/testing.md).

**Worker:** `STOPPED → STARTING → IDLE`, then `IDLE → STARTING → SPECTATING → STOPPING → IDLE`.
STARTING does python-steam login → match-ID resolution (rich-presence `WatchableGameID`) → GUI
Steam login → Dota launch → **GUI-automated spectate join** (friends panel → right-click target →
Spectate, via the uinput mouse; there is no console join command) → FFmpeg start. STOPPING tears
those down. A Steam Guard interrupt during STARTING sends
`SteamGuardRequired`, pauses login, waits for `SubmitSteamGuardCode`, then resumes.

**Session (control plane):**
```
OFF ─POST /api/sessions (StartSpectate sent)→ STARTING
STARTING ─StreamStarted→ WATCHING (webrtc_url set, pushed via WS)
WATCHING ─DELETE /api/sessions/:id or fatal ErrorEvent→ STOPPING (StopSpectate sent)
STOPPING ─StatusUpdate IDLE→ OFF
```

---

## Repository Structure

```
proto/spectator/v1/worker.proto   gRPC contract (source of truth)

control-plane/
  cmd/server/main.go
  internal/auth/        JWT, Argon2id hashing, credential encryption, key cache
  internal/sessions/    Session state machine, lifecycle
  internal/workers/     Worker pool (V1: single), gRPC stream handler
  internal/store/       PostgreSQL access (pgx)
  internal/api/         HTTP handlers (Chi), WebSocket hub
  internal/testdb/      Throwaway-DB harness for tests
  db/migrations/        001_init.sql, …
  docs/                 Generated OpenAPI spec (do not edit)
  gen/                  Generated protobuf Go code (do not edit)

worker/                 uv project (Python 3.10); see docs/worker.md
  agent.py grpc_client.py state_machine.py steam_client.py steam_gui.py dota_client.py ffmpeg.py
  xorg/xorg.conf  gen/  pyproject.toml  .python-version

frontend/               Vite + React SPA (Vitest + MSW tests)
  index.html  vite.config.js  package.json
  src/  App.jsx main.jsx api.js ws.js auth.js status.js webrtc.js styles.css
        pages/ (Login, Friends, Watch)  components/ (SteamGuardModal, AccountLink)
        __tests__/  test/ (fixtures.js, setup.js)
mediamtx/mediamtx.yml   nginx/nginx.conf   docker-compose.yml
docs/                   Extended reference (see top of this file)
```

The control plane has no `internal/steam/` Web-API client — friends come from the worker.

---

## V1 Scope

**In V1:** registration/login; one linked Steam account per user; friends list with in-match
status from the warm in-process python-steam session (kept alive between calls, dropped during
spectate); start/stop spectating (single worker, single session); Steam Guard interactive flow;
WebRTC stream in browser; error display.

**Deferred to V2:** WARM state for Dota/GUI Steam (keeping the game alive between sessions — the
friends-session warmth above is V1); live presence *push* to the browser (V1 pulls); multiple
concurrent sessions + worker pool; frame interpolation (nvinterpolate); match session sharing;
Kubernetes; PWA/mobile; AI-assisted crash recovery.

---

## Implementation Order

Steps 1–6 are complete (proto, migrations, control-plane + worker skeletons, auth, friends);
step 7 is complete — match-ID resolution is **validated** (via rich presence, not the GC; see
worker section) and **wired into `steam_client.py`** (`resolve_match_id` /
`extract_watchable_match_id`) + `agent.py` (`MatchIdResolved`). Step 10 (frontend) is complete.
**Steps 8–9 (worker spectate path) are implemented in the worker** — the GUI spectate automation
(`dota_client.py`), GUI-Steam bring-up (`steam_gui.py`), and the FFmpeg encoder + `StreamStarted`
(`ffmpeg.py`, `agent.py`) — gated behind `WORKER_DOTA_BRINGUP=1` pending the live container stack
(step 11). Their **control-plane counterpart is wired** too: the four `/api/sessions` handlers drive
`internal/sessions` (a `Manager` over the `state.go` machine) which sends StartSpectate/StopSpectate
to the worker, reacts to its `StreamStarted`/`StatusUpdate IDLE`/`ErrorEvent`/`MatchIdResolved`/
session `SteamGuardRequired` events, persists rows (`SessionStore`), and pushes `session_state`/
`stream_ready`/`error`/`steam_guard` over WS. **Steps 11–12 (deployment) are scaffolded:**
`docker-compose.yml` (postgres + control-plane + mediamtx + GPU worker), `control-plane/Dockerfile`
(distroless Go) with **boot-time idempotent migrations** (`store.Migrate`), `worker/Dockerfile`
(on the validated `dota-steam` base + supervisord), `mediamtx/mediamtx.yml`, and `nginx/nginx.conf`
(host TLS + proxy). The data plane is reviewable/testable; the **worker GPU image needs live
validation on the server** (uinput device-enumeration ordering — see deployment.md). Known-risks
have been validated live on the server (see [docs/validation-results.md](docs/validation-results.md)):
V1 headless Xorg/NVIDIA, V2 Dota install, V3 match-ID, V6 NVENC/SRT, and **V4 headless GUI-Steam
QR login + silent auto-login** all pass; **V5** has Dota launch + render + Steam auth + the **input
path** (uinput keyboard + absolute mouse via libinput) passing, with only live **spectate
initiation** outstanding. That initiation is **GUI automation, not a console command**:
`dota_spectate_game` was found **not to exist** (no console command joins a live match by id), so a
friend spectate is started by driving **friends panel → right-click friend → Spectate** with the
uinput mouse (team-vision-only via Dota Plus; GC automation rejected — see
[docs/validation-results.md](docs/validation-results.md) V5).

1. `proto/worker.proto` — finalise and generate Go + Python code first ✓
2. `db/migrations/` — schema only ✓
3. Control-plane skeleton — gRPC accepts connections, HTTP returns 501s, WS hub compiles ✓
4. Worker skeleton — gRPC client connects, state machine logs transitions ✓
5. Auth — register/login, Argon2id, JWT, AES-256-GCM credential storage ✓
6. Friends — key cache, account linking, `ListFriends`/`FriendsResult`, worker-backed
   `/api/friends`, worker's warm python-steam friend listing ✓
7. Worker Steam — python-steam login (shared with friends), refresh-token acquisition via
   `LinkAccount` (QR or credentials handshake; token persisted encrypted, no sentry/`login_key`) ✓;
   match-ID resolution via rich presence `WatchableGameID` (no GC, no `dota2` dep), wired into
   `steam_client.py` on the warm session ✓
8. Worker Dota automation — headless launch (sniper wrapper + headless GUI-Steam auth, V4/V5 ✓);
   input path (uinput keyboard + absolute mouse via libinput, V5 ✓); spectate initiation via **GUI
   automation** (friends panel → right-click friend → Spectate; **no console join command exists**),
   then camera via in-session console commands (`dota_spectator_mode`, `spec_player`, …)
9. Worker FFmpeg pipeline — x11grab → hevc_nvenc → SRT → mediamtx
10. Frontend — Login, Friends, Watch pages, SteamGuardModal/AccountLink, WebSocket integration;
    decision-logic unit tests (Vitest + MSW) ✓
11. Docker Compose — `docker-compose.yml` (postgres + control-plane + mediamtx + GPU worker),
    `control-plane/Dockerfile` (+ boot migrations), `worker/Dockerfile` (on `dota-steam` +
    supervisord) ✓ scaffolded; **worker GPU image needs live validation** (uinput ordering)
12. nginx + TLS — `nginx/nginx.conf` (host TLS + `/api` `/ws` `/webrtc` proxy + static SPA) ✓
    written; certbot cert issuance + live bring-up on the server remain

Before the worker spectate path (steps 7–9), validate the items in
[docs/known-risks.md](docs/known-risks.md) on the real server — results recorded in
[docs/validation-results.md](docs/validation-results.md).

---

## Testing

Test code that makes decisions; skip glue and stubs. `make test` runs Go (`make test-go`, real
PostgreSQL via an ephemeral cluster) + Python (`make test-py`, `uv run pytest` under Python
3.10) + frontend (`make test-fe`, Vitest + MSW). State machines, routing, serialization
contracts, and crypto are the priorities. Full strategy and per-area coverage in
[docs/testing.md](docs/testing.md).

---

## Commit Discipline

Prefer small, single-purpose commits, each independently revertable. Don't bundle unrelated
changes. Tests land with the code they cover (or as a focused follow-up). A reader should be able
to `git revert` any single commit without untangling others. Use conventional-commit prefixes
(`feat:`, `fix:`, `refactor:`) and state what the commit does, not lengthy justifications.
