# Dota 2 Spectator-as-a-Service

A self-hosted service that lets users spectate live Dota 2 matches played by their Steam
friends from any device. Authenticate, link your Steam account, pick a friend
who's currently in a match, and watch their game as a low-latency WebRTC stream.

## Architecture
- **Control Plane (Go)** — auth (JWT), Steam account management, session lifecycle, gRPC server.
  Friends and in-match status are pulled from the worker's authenticated Steam session over a
  bidirectional gRPC stream. Steam passwords are encrypted at rest with a key derived from the user's login password 
  (never written to disk).
- **Worker (Python)** — logs into Steam, resolves the target's live match from Steam rich
  presence (`WatchableGameID`, not the Game Coordinator), drives Dota on a headless Xorg display,
  and runs the FFmpeg capture/encode pipeline.
- **mediamtx** — ingests SRT from the worker, serves WebRTC (WHEP) to the browser.
- **Frontend** — Vite + React SPA: login/register, friends list with Spectate buttons,
  fullscreen WebRTC watch page, and an interactive Steam Guard flow.
- **nginx** — TLS termination and reverse proxy.

## Tech Stack

| Layer         | Stack                                                                   |
|---------------|-------------------------------------------------------------------------|
| Control plane | Go · Chi · gRPC · pgx · nhooyr.io/websocket · Argon2id                  |
| Worker        | Python 3.10 (uv) · python-steam · protobuf 3.20 · FFmpeg (video capture)|
| Media         | mediamtx (SRT in, WebRTC out)                                           |
| Frontend      | Vite · React · react-router-dom · Vitest + MSW                          |
| Data          | PostgreSQL                                                              |
| Infra         | Docker Compose · nginx                                                  |

## Repository Layout

```
proto/          gRPC contract (source of truth)
control-plane/  Go service (cmd/, internal/, db/migrations/, gen/, docs/)
worker/         Python worker (uv project)
frontend/       Vite + React SPA
mediamtx/       Media server config
docs/           Extended reference (grpc-contract, database, worker, deployment, …)
```

## Getting Started

### Prerequisites

- Go, Node.js + npm, [uv](https://docs.astral.sh/uv/) (manages Python 3.10), and Docker.
- An NVIDIA GPU with drivers for the worker's NVENC pipeline.

### Configuration

```sh
cp .env.example .env
# Fill in JWT_SECRET, CREDENTIAL_PEPPER, etc.
openssl rand -hex 32   # for each secret
```

### Code generation

```sh
make proto-tools   # one-time: install Go codegen tools + uv sync
make proto         # regenerate Go + Python gRPC stubs
make docs          # regenerate the OpenAPI spec (swaggo)
```

### Running

Bring the full stack up with Docker Compose:

```sh
docker compose up --build
```

The control plane serves `/api` and `/ws` on `:42000` and gRPC for workers on `:42010`
(see `.env.example`). API docs are served at `/docs`.

## Testing

```sh
make test       # control plane + worker + frontend
make test-go    # Go tests against an ephemeral PostgreSQL cluster
make test-py    # worker tests (uv run pytest)
make test-fe    # frontend tests (Vitest + MSW)
```

Tests focus on decision logic: state machines, routing, serialization contracts, and crypto.
See [docs/testing.md](docs/testing.md) for the full strategy.

## Documentation

- [gRPC contract](docs/grpc-contract.md)
- [Database schema](docs/database.md)
- [Worker internals](docs/worker.md)
- [Deployment](docs/deployment.md)
- [Known risks](docs/known-risks.md)
- [Testing](docs/testing.md)
