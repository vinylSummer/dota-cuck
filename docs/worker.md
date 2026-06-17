# Worker (Python)

Single process. Connects to the control plane gRPC server on startup, opens the
bidirectional `WorkerSession` stream, receives Commands and sends Events, and drives the
worker state machine.

## Runtime: Python 3.10 + protobuf-3.20 line, via uv

python-steam (`steam[client]==1.4.4`) pins `protobuf~=3.0`. Stubs generated with a modern
`grpcio-tools` import `runtime_version` and need protobuf ≥5 — incompatible. **Resolution:
pin the whole worker to the protobuf-3.20 line** and generate stubs with `grpcio-tools==1.48.2`
(the last release that emits protobuf-3 gencode and ships cp310 wheels). python-steam, grpcio,
the stubs, and gevent then coexist in one Python 3.10 process — verified end to end. No
subprocess bridge.

The worker is **uv-managed** (`worker/pyproject.toml`, `worker/.python-version` = 3.10). uv
fetches the 3.10 interpreter and the deps, so no system Python 3.10 is required. `make test-py`
and `make proto` invoke `uv run`. Pins: `protobuf==3.20.3`, `grpcio==1.48.2`,
`steam[client]==1.4.4`; dev: `grpcio-tools==1.48.2`, `setuptools<81` (grpc_tools.protoc imports
`pkg_resources`), `pytest`.

## Responsibilities

- Steam login: python-steam for match-ID resolution + friends; GUI Steam for Dota automation. Set
  `set_credential_location` so the **sentry** persists (Steam Guard device trust). **Sentry only —
  the `login_key` is never persisted** (`new_login_key` is not handled): the login_key is a full
  relogin secret, so cold logins re-send the password and rely on the sentry to skip the guard.
- **Account link** (`LinkAccount` command → `LinkResult` event): a standalone login that
  establishes the sentry and reports the account's `steam_id`. It drives the interactive Steam
  Guard flow — `SteamSession.link` pauses on a guard challenge, the agent emits
  `SteamGuardRequired` (correlated by `request_id`), and the worker resumes once
  `SubmitSteamGuardCode` delivers a code (`submit_guard_code`). Resolving the guard here means
  later friends/spectate logins reuse the sentry and don't re-prompt.
- **Warm friends session** (`steam_client.SteamSession`): one in-process python-steam session,
  lazily connected on the first `ListFriends`, kept alive (logged on) between calls. Lists the
  logged-in account's friends with online + in-match status (`ListFriends` command →
  `FriendsResult` event) and reports its own `steam_id` so the control plane can backfill
  `steam_accounts.steam_id`. **Dropped while spectating** (GUI Steam needs the account — see the
  dual-session risk in [known-risks.md](known-risks.md)) and re-warmed lazily afterwards. The
  handler runs off the command-stream thread so a slow Steam reply doesn't block other commands.
- Resolve the live match ID of a target Steam ID from the warm python-steam session's **rich
  presence** (`request_persona_state(ids, state_flags=… | 0x200)` → `rich_presence['WatchableGameID']`).
  Validated 2026-06-15: the Dota Game Coordinator (python-dota2) does **not** connect for a session
  not running the game, so there is no GC query and **no `dota2` dependency** (see
  [validation-results.md](validation-results.md) V3). Still to be wired into `steam_client.py`.
- Launch and automate Dota 2 on headless Xorg (`DISPLAY=:99`); join in spectator mode; select
  player-follow camera via console commands.
- Run the FFmpeg pipeline (see [deployment.md](deployment.md)).
- Persist the sentry file after first login; report all state transitions and errors upstream.

## Libraries

`steam` (python-steam) for login, friends, and rich-presence match-ID resolution; `grpcio` for
the stream; `pyautogui` / `python-xlib` as a GUI-automation fallback if needed. **No `dota2`
(python-dota2) dependency** — the GC is not used for match-ID resolution (see V3 in
[validation-results.md](validation-results.md)); keep it out of the worker deps unless the V5
spectate join turns out to need it.

## Modules

| File | Role |
|------|------|
| `agent.py` | Entry point, state-machine driver, command handlers, Friends/Link/guard event mapping |
| `grpc_client.py` | Bidirectional stream + `CommandDispatcher` (pure command routing) |
| `state_machine.py` | Pure worker state-machine table |
| `steam_client.py` | Warm python-steam session, sentry-only login + interactive Steam Guard, `derive_status` (friends); rich-presence `WatchableGameID` match-ID resolution still to be added |
| `dota_client.py` | GUI Dota automation — launch, join, camera (stub) |
| `ffmpeg.py` | FFmpeg subprocess management (stub) |
| `xorg/xorg.conf` | Headless NVIDIA display config (fill in BusID) |
| `gen/` | Generated protobuf Python code — do not edit |
