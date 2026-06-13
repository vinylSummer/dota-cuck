# Testing

**Philosophy:** test code that makes decisions; skip glue and stubs. A handler that returns
`501`, or a Steam/Dota/FFmpeg call with no logic yet, has nothing to assert. Introduce tests
for each piece *when it gains real behaviour*. State machines, request routing, serialization
contracts, and crypto are what's worth covering.

**Design constraint:** the session and worker state machines are **pure transition functions /
tables** (`Next(cur, event) (next, error)`), not logic buried in handlers — the right shape
regardless, and what makes them testable.

## Running

`make test` → `make test-go` + `make test-py` + `make test-fe`.

- **Go (`make test-go`)** wraps the run in `scripts/with-test-db.sh`. DB-backed tests run
  against **real PostgreSQL**, not a mock — anything touching the DB (the `store` package and
  auth HTTP handlers) requires an instance at `POSTGRESQL_URL` and **fails loudly** without it
  (no skips). The script spins up an ephemeral cluster (`initdb` + `pg_ctl`, unix-socket only,
  torn down after) and sets `POSTGRESQL_URL`. `internal/testdb` gives each test a fresh
  throwaway database with all migrations applied. Set `POSTGRESQL_URL` yourself to use an
  existing instance; set `PG_BINDIR` if `initdb`/`pg_ctl` aren't on `PATH` (e.g. Debian).
- **Python (`make test-py`)** runs `uv run pytest` in `worker/`. uv provisions Python 3.10 and
  the protobuf-3.20 deps (see [worker.md](worker.md)); no system Python 3.10 needed.
- **Frontend (`make test-fe`)** runs `npm test` (Vitest) in `frontend/`, installing deps on
  first run if `node_modules` is absent. No backend or browser required — the HTTP boundary is
  mocked with MSW.

## Coverage by area

Control plane (Go, stdlib `testing`, table-driven):
- **Session state machine** (`internal/sessions`): every valid edge advances
  (`OFF→STARTING→WATCHING→STOPPING→OFF`); invalid edges error; a fatal-error event from any
  active state routes to `STOPPING`.
- **HTTP router contract** (`internal/api`, `httptest`): documented routes registered; unknown
  paths `404`. Locks the API surface.
- **WebSocket push-event marshaling** (`internal/api`): the four events marshal to exactly the
  JSON shape the frontend depends on.
- **gRPC `WorkerSession` handler** (`internal/workers`): in-memory bidi stream — worker
  connects, sends `WorkerReady`, is registered; a pushed `Command` reaches the stream.
- **Auth/crypto** (step 5): Argon2id + AES-256-GCM — the most important tests in the project.

Worker (Python, `pytest`):
- **Worker state machine**: parametrized valid/invalid transitions.
- **Command dispatch** (`grpc_client.py`): each `Command` oneof variant routes to the correct handler.
- **Friends** (`steam_client.derive_status`, `agent` FriendsResult mapping + ListFriends
  routing): pure logic with a faked Steam session. The python-steam glue in
  `steam_client.SteamSession` is validated on-server, not unit-tested.

Frontend (JS, Vitest + MSW):
- **WS event routing** (`ws.routeEvent`): each event type classifies correctly and the
  account- vs session-scoped `steam_guard`/`error` variants discriminate by which id is present;
  unknown types are ignored, not thrown.
- **API request contract** (`api.js`, MSW): each call hits the documented method/path with the
  bearer header and correct JSON body; `getFriends` surfaces `409` (no account) and `502`
  (worker) distinctly; the guard submit surfaces `404`/`409`. Fixtures mirror the CLAUDE.md
  WS/REST shapes verbatim — the WS strings are the same JSON the Go WS-marshaling test asserts.
- **Auth/status** (`auth.isAuthed`, `status.canSpectate`): pure predicates. The React render
  trees, WHEP negotiation (`webrtc.js`), and the socket/`fetch` lifecycle are glue, not tested.
