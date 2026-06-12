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

- Steam login: python-steam for GC queries + friends; GUI Steam for Dota automation. Set
  `set_credential_location` so sentries persist; capture the `login_key` (`new_login_key` event)
  for password-less `relogin()` on later logins.
- **Warm friends session** (`steam_client.SteamSession`): one in-process python-steam session,
  lazily connected on the first `ListFriends`, kept alive between calls (relogin via `login_key`).
  Lists the logged-in account's friends with online + in-match status (`ListFriends` command →
  `FriendsResult` event) and reports its own `steam_id` so the control plane can backfill
  `steam_accounts.steam_id`. **Dropped while spectating** (GUI Steam needs the account — see the
  dual-session risk in [known-risks.md](known-risks.md)) and re-warmed lazily afterwards. The
  handler runs off the command-stream thread so a slow Steam reply doesn't block other commands.
- Query the Dota 2 Game Coordinator for the live match ID of a target Steam ID.
- Launch and automate Dota 2 on headless Xorg (`DISPLAY=:99`); join in spectator mode; select
  player-follow camera via console commands.
- Run the FFmpeg pipeline (see [deployment.md](deployment.md)).
- Persist the sentry file after first login; report all state transitions and errors upstream.

## Libraries

`steam` (python-steam) + `dota2` (python-dota2) for GC; `grpcio` for the stream;
`pyautogui` / `python-xlib` as a GUI-automation fallback if needed.

## Modules

| File | Role |
|------|------|
| `agent.py` | Entry point, state-machine driver, command handlers, FriendsResult mapping |
| `grpc_client.py` | Bidirectional stream + `CommandDispatcher` (pure command routing) |
| `state_machine.py` | Pure worker state-machine table |
| `steam_client.py` | Warm python-steam session, `derive_status`, GC match-ID + sentry (step 7) |
| `dota_client.py` | GUI Dota automation — launch, join, camera (stub) |
| `ffmpeg.py` | FFmpeg subprocess management (stub) |
| `xorg/xorg.conf` | Headless NVIDIA display config (fill in BusID) |
| `gen/` | Generated protobuf Python code — do not edit |
