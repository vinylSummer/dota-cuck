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
| V4 Dual-session handoff + Steam Guard | python-steam→GUI Steam handoff guard behavior | ⏳ PARTIAL — phase 1 (python-steam handoff) PASS; GUI-Steam phase pending |
| V5 Dota spectate console command | Exact sequence to join a live match + follow camera | ⏳ needs Steam creds + Dota + live match |
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
  the GUI Steam **sentry/device-trust must be established once and must suppress the confirmation
  on subsequent logins**, or first-time spectate setup needs a human approval. Confirm in V4.
- **Security note:** `steamcmd +login user pass` exposes credentials in the process list. The
  real worker passes creds over gRPC (never argv); this is a harness-only artifact. Rotate the
  test-account password after validation.

## V4 — Dual-session handoff + GUI-Steam Steam Guard — ⏳ PARTIAL (phase 1 PASS)

Probe: `v4_dual_session.sh` (+ `v4_steam_phase.py`). Needs live creds (`~/.dota-validation.env`).
Run while you can complete a one-time mobile confirmation on your phone. Container
`HOME=/fard/steam/steamhome` (sentries persist on the ZFS dataset).

- [x] **Phase 1 PASS (2026-06-17)** — python-steam logged in as STEAM_USER (`vinyl summer`,
      `76561198179568701`), saw 194 friends, and logged out cleanly (`PYTHON_STEAM_LOGGED_OUT`),
      vacating the account for the GUI client. Needed one mobile Steam Guard code.
- [ ] **GUI-Steam first login**: did Steam Guard / a mobile-confirmation **tap** fire? Tap
      (uncompletable headless, needs a one-time human) or a typed code? — _not yet observed_
- [ ] After completing the first login, is the **next** GUI-Steam login **silent** (device trusted)?
- [ ] Only **one** session on the account at a time — no "logged in elsewhere" kick after handoff?
- [ ] Implication for route A: if a guard genuinely fires per-login, the session SM must surface a
      second session-scoped `SteamGuardRequired` (empty `request_id`).

> **Blocker found (2026-06-19):** even with the full headless GUI-Steam launch recipe working
> (steamwebhelper runs on hardware GL — non-root + seccomp/apparmor unconfined + bind-mounted
> 32-bit NVIDIA GL + dbus + zenity stub), the **modern Steam login UI never renders headless**
> (CEF login popup fails: `Failed to create popup` / `Cannot read properties of undefined`).
> Interactive GUI-Steam login is not viable headless. This motivates seeding a refresh
> token for silent auto-login, dovetailing with the refresh-token auth model. Architecture
> decision pending.

Screenshots: `/fard/steam/steamhome/v4-shots/`.

---

## V5 — Dota spectate console command + camera follow — ⏳ PENDING

Probe: `v5_spectate.sh`. Preconditions: V4 passed (silent GUI-Steam login), Dota at
`/fard/steam/dota`, a **fresh** `WatchableGameID` (re-run `v3_gc_matchid.py` while a friend is in a
live watchable match; it writes `~/.dota-validation.matchid`).

Record (Task C copies this **verbatim**):

- [ ] Exact **launch options** that authenticate + render cleanly headless on `:99`
      (probe tries `steam -applaunch 570 -novid -console -nosound -nopreload`).
- [ ] Exact **spectate-join** command + how issued (xdotool keystrokes into the `` ` `` console).
      Candidate tried: `dota_spectate_game <WatchableGameID>`. Working command: _(fill in)_
- [ ] Exact **camera-follow** command(s). Candidates tried: `dota_spectator_mode 1`,
      `spec_player 0`, `dota_spectator_autofollow 1`. Working command: _(fill in)_
- [ ] **Spectator delay** observed (affects `stream_ready` timing).
- [ ] Any match-watchability gotchas (private/non-watchable, delay before joinable).
- [ ] Confirmed the captured `:99` frame is the **moving live match** (not the menu) —
      `v5-shots/v5_capture.mp4` + screenshots.

Screenshots + capture: `/fard/steam/steamhome/v5-shots/`. _Findings:_ _(fill in)_

