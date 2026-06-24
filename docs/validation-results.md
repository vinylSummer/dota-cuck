# Known-Risks Validation Results

Live validation of [known-risks.md](known-risks.md) on the target server before writing the
worker spectate path (CLAUDE.md steps 7-GC / 8 / 9). Probes live in `scripts/validation/`.

**Target:** `wolf-den` (192.168.1.53), Debian 13 (trixie, kernel 6.12.90), RTX 3090,
NVIDIA driver **610.43.02**, Docker 29.5.2 with the `nvidia` runtime + CDI, user `vinyl` in
the `docker`/`video` groups (host `sudo` requires a password, so all GPU/Steam work runs in
containers). GPU host PCI address **0b:00.0** → Xorg **`PCI:11:0:0`**.

| Item | What it proves | Status |
|------|----------------|--------|
| V1 Headless Xorg + NVIDIA GLX in Docker | Worker can render on a GPU-backed headless `:99` | ✅ PASS |
| V2 Steam + Dota install via steamcmd | Dota installable into a named volume; update strategy | ✅ PASS (72G logical / 44G on ZFS, /fard/steam) |
| V3 Match-ID resolution (python-steam rich presence) | Resolve a live match ID for a target steam_id | ✅ PASS (via rich presence, not GC) |
| V4 Headless GUI-Steam login (QR + silent auto-login) | A worker can log the GUI Steam client in headless, once, then auto-login silently | ✅ PASS |
| V5 Dota launch + spectate | Launch Dota headless (steamcmd-managed install), authenticate, render; join a live match | ✅ PASS (2026-06-24) — launch + render + Steam auth + input path + **GUI spectate** all proven; right-click friend → WATCH FRIEND LIVE → live match in **player view** (no console join command exists; no camera-follow needed) |
| V6 FFmpeg x11grab → hevc_nvenc → SRT → mediamtx | NVENC on headless Xorg; SRT path to mediamtx | ✅ PASS to mediamtx (browser WebRTC leg = human check) |

---

## Tooling notes (probe gotchas — save the next runner the debugging)

- **X readiness: use `xset q`, not `xdpyinfo`.** `xdpyinfo` is in `x11-utils`, which is NOT in the
  validation images (they install `x11-xserver-utils` → `xset`/`xrandr`, and `glxinfo` via
  `mesa-utils`). Polling `xdpyinfo` silently fails every iteration even when X is up. (V1's script
  only "passed" because it runs `glxinfo` regardless of the poll.) The V4/V5 probes use `xset q`.
- **`steam` / `steamcmd` live in `/usr/games`**, which is not on the default non-login `PATH`.
  `Dockerfile.steam` adds `ENV PATH=/usr/games:$PATH` so they resolve inside `bash -c`.
- **`steam-installer` / `steamcmd` gate on a debconf license prompt** — `Dockerfile.steam`
  pre-accepts it (`debconf-set-selections`) so the non-interactive build doesn't hang.
- **Read screenshots with OCR, not a vision model.** `scripts/validation/ocr.sh <shot|abs-path>`
  runs `imagemagick` preprocessing + `tesseract` inside the container against a shot on the shared
  `/fard/steam` volume. **Use `PSM=6` for modal/dialog text** (default 11 is for sparse text and
  mangles dialog titles). Known modal OCR signatures (anchor strings for state detection) are
  catalogued in the worker-spectate progress memory: "Update Required" → `out of date`; "Player
  Behavior Summary" → `PLAYER BEHAVIOR SUMMARY`; "Party Invitation" → `PARTY INVITATION`; bare
  dashboard/ready → `PLAY DOTA` (seasonal strings like Quartero's/Overwatch/Collector's Cache are
  NOT stable; the Dota font also makes OCR swap R→K / T→I on the nav tabs).
- **Ground-truth any console command before relying on it** — grep the game libs
  (`grep -aoE 'dota_[a-z_]*' game/dota/bin/linuxsteamrt64/lib{client,server}.so`) or run
  `find <substr>` in-console (`-condebug` → `console.log`). The planned `dota_spectate_game` /
  `dota_spectator_autofollow` turned out **not to exist** (see V5).

---

## V1 — Headless Xorg + NVIDIA GLX inside a container — ✅ PASS

Probe: `scripts/validation/v1_headless_gpu.sh` (image `scripts/validation/Dockerfile.xtest`).

Built a minimal Debian image with `xserver-xorg-core` + `mesa-utils` (no NVIDIA driver
package — the NVIDIA Container Toolkit injects `nvidia_drv.so` + GLX libs when the container
requests `NVIDIA_DRIVER_CAPABILITIES=all`). Started `Xorg :99` with the worker's
[xorg.conf](../worker/xorg/xorg.conf) (`BusID PCI:11:0:0`,
`AllowEmptyInitialConfiguration`, `Virtual 1280 720`) and ran `glxinfo`.

Result — hardware rendering on the headless display:
```
direct rendering: Yes
OpenGL vendor string:   NVIDIA Corporation
OpenGL renderer string: NVIDIA GeForce RTX 3090/PCIe/SSE2
OpenGL core profile version string: 4.6.0 NVIDIA 610.43.02
Dedicated video memory: 24576 MB
```

**Design implications / confirmed facts:**
- `worker/xorg/xorg.conf` BusID `PCI:11:0:0` is correct for this box (derive from
  `nvidia-smi --query-gpu=pci.bus_id` on any other host).
- The worker container must run with `--gpus all` and **`NVIDIA_DRIVER_CAPABILITIES=all`**
  (or at least `graphics,display,video,compute,utility`); do **not** install an NVIDIA
  driver package in the worker image. Carry this into the worker `Dockerfile` (step 11).
- `Xwrapper.config` `allowed_users=anybody` is needed for Xorg to start as container root.

---

## V6 (NVENC half) — FFmpeg x11grab → hevc_nvenc on headless :99 — ✅ PASS

Probe: `scripts/validation/v6_nvenc.sh`. Rendered `glxgears` on `:99`, captured 5s with the
[deployment.md](deployment.md) FFmpeg sketch (`-f x11grab -r 60 -s 1280x720 -i :99 -c:v
hevc_nvenc -preset p4 -b:v 4M`), muxed to mpegts.

Result — full-rate hardware encode of the headless display:
```
frame=  300 fps= 60 q=8.0 Lsize=1406KiB time=00:00:04.95 bitrate=2326.5kbits/s speed=0.991x
output bytes: 1439516
```

**Design implications / confirmed facts:**
- NVENC (`hevc_nvenc`) initializes and encodes a virtual-framebuffer capture at 60fps — no CPU
  fallback. The `video` driver capability (covered by `NVIDIA_DRIVER_CAPABILITIES=all`) is
  required for NVENC in the container.
- The deployment.md FFmpeg arg set is correct as written; `ffmpeg.py` (step 9) can adopt it.
### V6 (SRT/mediamtx leg) — ✅ PASS

Probe: `scripts/validation/v6_srt_mediamtx.sh` + [mediamtx config](../mediamtx/mediamtx.yml).
Ran mediamtx, captured `:99`, NVENC-encoded, and pushed over SRT; mediamtx logged the
publisher:
```
INF [path live/match] stream is available and online, 1 track (H265)
INF [SRT] [conn 127.0.0.1:45327] is publishing to path 'live/match'
```

**Design implications / confirmed facts:**
- The full **x11grab → hevc_nvenc → SRT → mediamtx** path works at real-time (speed ≈ 1.0x,
  60fps, ~2.3 Mbps H.265). `ffmpeg.py` (step 9) can use this verbatim.
- **The mediamtx SRT streamid must be `publish:live/match`** — deployment.md's bare
  `streamid=live/match` is publish-ambiguous; fix the worker's SRT URL accordingly.
- `mediamtx/mediamtx.yml` created (api on :9997, srt on :8890, webrtc/WHEP on :8889, single
  path `live/match`). The container runs fine on the host network.
- **Remaining (human-only):** open `https://<host>:8889/live/match` (WHEP) in a browser to
  confirm the moving video renders. Everything up to mediamtx is proven.

---

## V3 — Live match-ID resolution — ✅ PASS (important design change)

Probe: `scripts/validation/v3_gc_matchid.py` (run via `uv run --with steam[client]==1.4.4
--with dota2`). Logged in (mobile Steam Guard, interactive file handoff), enumerated
friends, requested persona state **with the rich-presence flag** (`0x200`), and read each
Dota friend's rich presence.

Result — the live match id comes straight from Steam **rich presence**:
```
candidate 76561199020767409 'zitraks mops'  WatchableGameID=29885347581173389  param0=#DOTA_lobby_type_name_ranked
candidate 76561198030100819 'tpaba vol. 847' WatchableGameID=29885347596779467  param0=#game_mode_23 (turbo)
...
GC ready=False     ← python-dota2 GC connection did NOT establish within 30s
V3 PASS: resolved live match id 29885347581173389
```

**Design implications / confirmed facts (this changes the worker design):**
- **The match id is the rich-presence `WatchableGameID`, not a Dota GC query.** It is present
  and >0 only for live, *watchable* public matches; it is absent (demo/private) or `0`
  (in party UI, not yet in match). The party blob also reports `party_state: IN_MATCH`.
  `param0` gives the lobby/game-mode label, `param2` the hero.
- **The Dota GC (python-dota2) never became ready** (`GC ready=False`) for a python-steam
  session that isn't actively running the game. So CLAUDE.md/worker.md's "query the GC for
  the match id" should be **revised to "read `WatchableGameID` from rich presence"** — simpler,
  and it reuses the existing warm friends session.
- **Reuse the warm session:** the worker's `ListFriends` path (`steam_client.py`) already holds
  the python-steam session; resolving the spectate target is just `request_persona_state(ids,
  state_flags=… | 0x200)` + reading `rich_presence['WatchableGameID']`. No separate GC
  subsystem and **no `dota2` dependency** are needed for match-ID resolution — fold this into
  `steam_client.py` instead of `dota_client.py`. (Keep `dota2` out of the worker deps unless the
  spectate join in V5 turns out to need it.)
- `WatchableGameID` is a ~58-bit value — fits the proto `MatchIdResolved.match_id` (`uint64`).
- **Caveat for the worker:** rich presence isn't populated instantly — the probe needed an
  explicit `request_persona_state` with the rich-presence flag and a few seconds' settle before
  `WatchableGameID` appeared.

---

## V2 — Steam + Dota install via steamcmd — ✅ PASS

Probe: `scripts/validation/v2_install.sh`.

**Disk finding (important for deployment):** the root fs (`/dev/sdb2`, 207G) is 98% full
(5.5G free) — far too little for Dota. The box has a ZFS pool with **`/fard/steam` (≈129G
free)**, the intended `steam-data` location, plus `/poop` (1.5T). The install targets
`/fard/steam/dota`. The worker `steam-data` volume (deployment.md) must be bound to
`/fard/steam`, **not** under `/` or the Docker root on `/`.

**Facts confirmed:**
- `steamcmd +force_install_dir /fard/steam/dota +login … +app_update 570 validate +quit`
  completed: `Success! App '570' fully installed.` (`appmanifest_570.acf` `StateFlags=4`).
- **Size:** `SizeOnDisk` 72,376,742,563 (≈72 GB logical); the content VPKs (`game/dota/pak01_*.vpk`)
  total ≈37 GB. On the ZFS dataset `fard/steam` (compression on, **1.65x**) it occupies **43.7 GB**
  physical (`logicalused 72.4G` / `used 43.7G`).
- The install is on the persistent ZFS dataset, so it **survives container rebuilds and host
  reboots**. steamcmd downloads are **resumable** — re-running the same `+app_update 570` continues
  from on-disk state. Validates the deployment.md "install once into a named volume" strategy:
  bind the worker `steam-data` volume to `/fard/steam`, run the install once, never at image build.
- **steamcmd login used Steam *Mobile Confirmation*, not a code** — Steam pushed an
  approve/reject prompt to the account's mobile device, which the operator accepted; steamcmd
  then proceeded (no code typed). This is **different from the python-steam friends/link flow**
  (V3), which used an entered mobile *code* (`two_factor_code`). Big V4 implication: a fresh GUI
  Steam login on the worker may demand a **device tap the headless worker cannot perform** — so
  the GUI Steam device-trust must be established once and must suppress the confirmation on
  subsequent logins, or first-time spectate setup needs a human approval. **Confirmed in V4:** the
  GUI client's one-time login is a **QR scan** (the headless UI renders the QR), after which the
  persisted token auto-logs-in silently with no further interaction.
- **Security note:** `steamcmd +login user pass` exposes credentials in the process list. The
  real worker passes creds over gRPC (never argv); this is a harness-only artifact. Rotate the
  test-account password after validation.

## V4 — Headless GUI-Steam login (QR) + silent auto-login — ✅ PASS (2026-06-19)

Probe: `v4_dual_session.sh {up|status|autologin|down}`. Container `HOME=/fard/steam/steamhome`
(the GUI client's encrypted token persists on the ZFS dataset).

The earlier blocker — the modern Steam login UI not rendering headless (CEF `Failed to create
popup` / `Cannot read properties of undefined`, all windows 10×10) — is **solved** by replicating
the `docker-steam-headless` GPU/desktop stack. Each of the following was necessary; partial fixes
still failed:

1. **Full Xfce4 session** (xfwm4 + compositor) on the X server — bare Xorg / lone openbox fail.
2. **Complete in-image NVIDIA driver** matching the host (the public `610.43.02` `.run`,
   `--no-kernel-modules --install-compat32-libs --no-install-libglvnd`) instead of the Container
   Toolkit's *partial* CDI injection — gives a self-consistent GL/EGL/Vulkan + 32-bit stack.
3. **Fake connected monitor** in `xorg.conf` (`scripts/validation/xorg.steam.conf`:
   `ConnectedMonitor "DFP-0"` + a `Modeline` + EDID-less `ModeValidation`). Without it
   steamwebhelper dies with `CreateOutputWindow: failed to create window: Could not find display
   info`. `xrandr` then reports `HDMI-0 connected`.
4. **`--shm-size=2g`** on `docker run` — Docker's default 64 MB `/dev/shm` is too small for Steam's
   CEF shared-memory IPC: `shmemstream.cpp … CSharedMemStream … 8192, 0` → `Failed to connect to
   master html process` → **Bus error (SIGBUS)**.
5. **System dbus** running (the `dbus` package, not just `dbus-x11`).

Also: Steam runs **non-root** (worker uid 1000, owns `$HOME`); `--security-opt
seccomp=unconfined,apparmor=unconfined`; `steam -no-browser` (no creds — `-login` is ignored).

- [x] **Phase 1 — interactive login renders headless.** The full Steam sign-in window (account
      fields **and a live QR code**) renders on `:99`, reachable over x11vnc/noVNC. Operator
      scanned the QR once with the Steam Mobile app → logged in; `loginusers.vdf` persisted
      (`rabsomera_awesome` / "vinyl summer" `76561198179568701`, `RememberPassword=1`,
      `AllowAutoLogin=1`), `connection_log.txt` → `Logged On`.
- [x] **Phase 2 — silent auto-login PASS.** A fresh container with **VNC off and no credentials**
      reaches `Logged On` from the persisted token, no Steam Guard. (`v4_dual_session.sh autologin`.)

**Design implications:**
- The GUI Steam client's token store is encrypted with an unpublished scheme — a python-steam
  refresh token **cannot** be seeded into it. So the GUI client needs its **own one-time
  interactive login**; thereafter the persisted token auto-logs-in silently. This is a *second*
  auth artifact alongside the user's refresh token (which serves friends + match-ID).
- **Per-user GUI login** (decided): each user's account logs into the GUI client once. The login's
  native QR will later be surfaced to the browser over WS (reusing the QR-primary link flow); VNC
  is the operator path for validation.
- The dual-session concern from [known-risks.md](known-risks.md) is moot for V1: the validation
  account is itself the spectator, so no python-steam↔GUI handoff is exercised here.

Recipe baked into `scripts/validation/{Dockerfile.steam,xorg.steam.conf,supervisord.conf,
desktop/*.sh}`. Screenshots: `/fard/steam/steamhome/v4-shots/`.

---

## V5 — Dota launch + spectate — ✅ PASS (launch + render + auth + GUI spectate proven)

Probe: `v5_spectate.sh {up|spectate|down}`. Reuses the V4 desktop stack + persisted silent login.

### Dota launch + render — ✅ PASS (2026-06-19)
`steamcmd +force_install_dir` produces a **flat** install (everything in `/fard/steam/dota`, no
`steamapps/common/<installdir>/`), so the GUI client's `-applaunch 570` can't see it. **Fix:**
launch directly through the install's own sniper wrapper (the same `_v2-entry-point` Steam uses per
`toolmanifest.vdf`), with the GUI Steam client running only for auth:
```
SteamAppId=570 /fard/steam/dota/run-in-sniper -- /fard/steam/dota/game/dota.sh -novid -console -nosound -nopreload
```
- This **keeps steamcmd managing the install** (`force_install_dir`, autoupdates) — no GUI library
  registration, no re-download, no layout change.
- Verified live: `dota2` runs inside `srt-bwrap`, `SteamAPI_Init(): Loaded steamclient.so OK`
  (authenticated against the headless Steam client), and the **Dota 2 main menu renders** on the
  RTX 3090 via Vulkan. First launch does a slow Fossilize Vulkan-pipeline precompile
  (`shadercache/570/fozpipelinesv6`, several min, ~250% CPU) before the window appears.

### Input injection — ✅ SOLVED (2026-06-23/24)
Source 2 **ignores XTEST** (what stock `xdotool`/x11vnc inject), so input is delivered through a
real kernel **uinput** device that **libinput** claims as genuine X input. The full recipe, all
necessary:
1. Install an Xorg input driver — `xserver-xorg-input-libinput` (+ `-evdev`, `xinput`); the base
   image had only `inputtest_drv.so`.
2. A persistent **uinput daemon** (`desktop/uinput_daemon.py`) creates the device(s) **before** Dota
   launches and owns them for the session.
3. `--device-cgroup-rule 'c 13:* rmw'` on `docker run` — without it libinput's `open(/dev/input/eventN)`
   gets **EPERM** even on a 0666 node.
4. dumb-udev mis-tags every virtual device `ID_INPUT_JOYSTICK`; `setup_uinput()` rewrites the udev
   `ID_INPUT_*` tag per device (keyboard vs mouse) and a `MatchProduct "dota-"` InputClass
   (`xorg-input.conf`) force-binds them to libinput.
5. **Restart Xorg once** after the device appears so its startup enumeration claims it via libinput.

Validated live: `xinput list` shows the devices as real keyboard/pointer; keystrokes reach Dota
(`[InputSystem] Processing SDL events`, `xinput test-xi2` shows real KeyPress detail); an **absolute
mouse** (`dota-vnc-mouse`, ABS_X/Y 0..32767 + `INPUT_PROP_POINTER`) maps the cursor 1:1 with no
acceleration (injecting `200,150` lands exactly at 200,150). The same path also backs a **VNC input
bridge** (`desktop/vnc_input_daemon.py` + x11vnc `-pipeinput`) so an operator's VNC mouse+keyboard
reach Dota for manual runs (`v5_spectate.sh manual` / `input`).

### Spectate-join — console join command does NOT exist; path is **GUI mouse automation** (✅ PROVEN 2026-06-24)
The planned `dota_spectate_game <id>` is a **dead end — that command does not exist** (verified
against Liquipedia's console-command list *and* by grepping this build's `libclient.so`/`libserver.so`;
`dota_spectator_autofollow` is likewise absent). **No console command anywhere joins a live match by
id** — every `dota_watch_*` token is Watch-tab UI/config tuning. Live spectating is **GC-mediated
through the GUI** (DotaTV). The in-game **Watch tab is tournaments/replays only** — not friends.

**Decision:** initiate a friend spectate by **automating the GUI** — open the friends panel,
right-click the friend who is in a live match, click **Spectate** — driven by the uinput **mouse**
(located via OCR-anchored clicks at fixed 1280×720, not hardcoded pixels). The native client then
does the GC watch handshake, SDR ticket, connect, and **render** itself; we only click. This is
**friend-spectate = team vision only** (Dota Plus, which the account has) — accepted for V1.

Rejected alternative — **GC automation** (python-dota2 / raw `CMsgWatchGame`): it returns *data, not
pixels* (can't render — the graphical client must still connect+render), needs a **second Dota GC
session the account can't grant** while the renderer is playing, was already proven not to connect
from a standalone python-steam session (V3, `GC ready=False`), isn't wired into python-dota2/node-dota2
(`request_top_source_tv_games` only finds *top* games, not a friend's pub), and a bare console
`connect` lacks the GC-issued SDR auth ticket. So it stays out (no `dota2` dep), consistent with V3.

**No in-session camera command is needed.** WATCH FRIEND LIVE lands directly in **player view** (the
friend's own camera), which is the desired V1 output. (The real in-session camera commands —
`dota_spectator_mode`, `spec_player <n>`, `spec_mode`, `spec_next`/`spec_prev`,
`dota_spectator_hero_index`, `spec_track`, `spec_goto`, all confirmed in `libclient.so` — are left
for a future free-camera/auto-follow mode, not V1.)

### Proven recipe (2026-06-24) — copied verbatim into `worker/dota_client.py` (Phase B)
Driven end-to-end from a clean dashboard to a moving NVENC capture of the live match in player view
(manual run 3.35 MB; fully-automated `gui_spectate.py spectate --target-name` 2.6 MB). Working path:
- [x] **GUI click path:** friends panel is **docked open on the LEFT** (`IN DOTA (n)` list at the
      top) → wheel **up to the top** (in-match friends are at the top; never down-scroll, it hides
      them) → **OCR-locate** the friend's row (psm 11, 3× upscale) → **right-click** the row → context
      menu (position is variable: over the left panel *or* to its right) → OCR the menu (inverted, 3×)
      → click **WATCH FRIEND LIVE** (Dota+ low-latency, preferred) or **WATCH GAME** (delayed
      fallback). First-login modals (`Update Required` non-fatal → OK, `Player Behavior Summary` →
      close-X, `Party Invitation` → ESC, `Welcome to Dota Plus` → dismiss) are cleared first.
- [x] **★ Dense-motion clicks are mandatory:** Panorama only registers a hover / opens a context menu
      if the pointer **arrives via continuous motion** (~15 interpolated points). A teleport-then-click
      reaches the X server but the UI does not react.
- [x] **Camera:** none — player view is the landing state (no follow command).
- [x] **Confirmed moving live match** in `v5-shots/v5_capture.mp4` (non-trivial NVENC render, not the
      menu). Needs a friend currently in a live, watchable match (fresh `WatchableGameID` via
      `v3_gc_matchid.py`).
- [ ] **Spectator delay** under WATCH FRIEND LIVE vs WATCH GAME (affects `stream_ready` timing) —
      not yet measured precisely; folded into the JOIN settle (~12 s) for now.

### Launch gating fix (2026-06-24)
Dota must not launch until **this run's** GUI Steam logon is confirmed, or it shows **"LOST
CONNECTION TO STEAM"** (no friends/account data). `connection_log.txt` is append-only across runs,
so the old whole-file `grep "Logged On"` matched a stale logon and let Dota start too early.
`v5_spectate.sh` now snapshots the log line count before launching Steam and only counts a logon
added since (and requires the latest transition to be `Logged On`), with a settle delay; `up`/`manual`
**abort** rather than launch Dota on an unconfirmed logon.

Screenshots + capture: `/fard/steam/steamhome/v5-shots/`.

