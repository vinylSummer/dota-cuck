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

- Steam login: python-steam for match-ID resolution + friends; GUI Steam for Dota automation.
  python-steam cold logins use the modern **refresh-token** model (`login_with_token`) — the worker
  logs onto the CM by putting the refresh token in the logon `access_token` field, with **zero
  Steam Guard interaction**. No sentry, no `login_key`; on token expiry/revocation the user
  re-links. The **GUI Steam client** is a *separate* auth artifact: its token store is encrypted
  with an unpublished scheme, so the refresh token **cannot** be seeded into it — it needs its own
  **one-time interactive (QR) login**, after which its persisted token auto-logs-in silently.
  Validated headless in V4 (see [validation-results.md](validation-results.md)); the GUI desktop
  recipe (Xfce4 + full in-image NVIDIA driver + fake-monitor xorg.conf + `--shm-size=2g` + dbus)
  lives in `scripts/validation/` and feeds the worker `Dockerfile` (step 11).
- **Account link** (`LinkAccount` command → `LinkResult` event): a standalone
  `IAuthenticationService` handshake that acquires the **refresh token** and reports the
  account's `steam_id`. Two modes: **QR** (no credentials) opens a QR session and emits
  `SteamQrChallenge` events carrying the URL to scan (rotated URLs reuse the `request_id`);
  **credentials** (email-only / no-2FA accounts) RSA-encrypts the password to Steam and, when an
  emailed code is required, drives the interactive Steam Guard flow — the agent emits
  `SteamGuardRequired` (correlated by `request_id`) and resumes once `SubmitSteamGuardCode`
  delivers a code (`submit_guard_code`). On success the worker returns the refresh token so the
  control plane encrypts and persists it; later friends/spectate logins reuse it and don't re-prompt.
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
  [validation-results.md](validation-results.md) V3). Wired into `steam_client.py`
  (`resolve_match_id` / `extract_watchable_match_id`) + `agent.py` (`MatchIdResolved`).
- Launch and automate Dota 2 on headless Xorg (`DISPLAY=:99`). **Initiating a spectate is GUI
  automation, not a console command** — there is **no console command that joins a live match by id**
  (`dota_spectate_game` does not exist; verified against the build's binaries — V5). The path is:
  drive the **friends panel → right-click the target friend (in a live match) → Spectate** via a
  libinput-bound uinput **mouse**, located with OCR-anchored clicks; the native client then does the
  GC watch handshake, SDR ticket, connect, and render. This is **team-vision-only** (Dota Plus) —
  accepted for V1. Once spectating, set the **camera** with the real in-session console commands
  (`dota_spectator_mode`, `spec_player <n>`, `spec_mode`, …) over the uinput **keyboard** (the `` ` ``
  console toggle is engine-special). Input delivery (uinput + libinput, both keyboard and absolute
  mouse) is validated — see [validation-results.md](validation-results.md) V5.
- Run the FFmpeg pipeline (see [deployment.md](deployment.md)).
- Report all state transitions and errors upstream.

## Libraries

`steam` (python-steam) for login, friends, and rich-presence match-ID resolution; `grpcio` for
the stream. GUI automation is delivered through a **uinput device** (keyboard + absolute mouse)
bound by libinput — Source 2 ignores XTEST, so `pyautogui`/`xdotool` (XTEST) do **not** work; screen
state is read with **tesseract OCR**, not a vision model. **No `dota2` (python-dota2) dependency** —
the GC is used neither for match-ID resolution (rich presence — V3) nor for spectate initiation (the
GUI client does the GC handshake itself; GC automation was rejected — see V5 / known-risks). Keep
`dota2` out of the worker deps.

## Modules

| File | Role |
|------|------|
| `agent.py` | Entry point, state-machine driver, command handlers, Friends/Link/guard event mapping |
| `grpc_client.py` | Bidirectional stream + `CommandDispatcher` (pure command routing) |
| `state_machine.py` | Pure worker state-machine table |
| `steam_client.py` | Warm python-steam session, refresh-token acquisition (QR/credentials handshake) + token CM login, interactive Steam Guard, `derive_status` (friends), rich-presence `WatchableGameID` match-ID resolution (`resolve_match_id` / `extract_watchable_match_id`), `persona_name` (cached, for GUI row matching) |
| `steam_gui.py` | GUI Steam client bring-up — silent auto-login (`dbus-run-session … steam`), wait for this run's CM logon before Dota launches; pure `is_logged_on` connection-log parser |
| `dota_client.py` | GUI Dota automation — in-process uinput devices (evdev), launch (sniper wrapper) + `wait_for_dota_window`, OCR-gated spectate via friends-panel right-click→WATCH FRIEND LIVE/GAME → player view (no camera-follow); pure decision logic (`classify_state`, `find_text_box`, …) split out for tests |
| `ffmpeg.py` | FFmpeg subprocess management (stub) |
| `xorg/xorg.conf` | Headless NVIDIA display config (fill in BusID) |
| `gen/` | Generated protobuf Python code — do not edit |
