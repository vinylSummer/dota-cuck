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
| V2 Steam + Dota install via steamcmd | Dota installable into a named volume; update strategy | 🟡 in progress (install to /fard/steam) |
| V3 Match-ID resolution (python-steam rich presence) | Resolve a live match ID for a target steam_id | ✅ PASS (via rich presence, not GC) |
| V4 Dual-session handoff + Steam Guard | python-steam→GUI Steam handoff guard behavior | ⏳ needs Steam creds |
| V5 Dota spectate console command | Exact sequence to join a live match + follow camera | ⏳ needs Steam creds + Dota + live match |
| V6 FFmpeg x11grab → hevc_nvenc → SRT → mediamtx | NVENC on headless Xorg; SRT path to mediamtx | 🟡 NVENC half ✅ PASS; SRT/browser leg pending |

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
- **Still pending (needs mediamtx up + a browser):** the SRT push to mediamtx
  (`-f mpegts "srt://mediamtx:8890?streamid=live/match"`) and WebRTC playback in the browser —
  see `scripts/validation/v6_srt_mediamtx.sh` (to be added).

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

## V2 — Steam + Dota install via steamcmd — 🟡 in progress

Probe: `scripts/validation/v2_install.sh`.

**Disk finding (important for deployment):** the root fs (`/dev/sdb2`, 207G) is 98% full
(5.5G free) — far too little for Dota. The box has a ZFS pool with **`/fard/steam` (≈129G
free)**, the intended `steam-data` location, plus `/poop` (1.5T). The install targets
`/fard/steam/dota`. The worker `steam-data` volume (deployment.md) must be bound to
`/fard/steam`, **not** under `/` or the Docker root on `/`.

**Facts confirmed so far:**
- Dota 2 (app 570) download size: **~68 GB** (73,179,594,568 bytes).
- Download rate on this box ≈ **8.6 MB/s → ~2.3 h** full install. Plan the worker image/volume
  so this runs **once into the persistent volume**, not per image build (validates the
  deployment.md "install into a named volume" strategy).
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

## V4 / V5 — blocked on V2

V4 (dual-session handoff + Steam Guard) and V5 (Dota spectate console command) need Dota
installed; they run after V2 completes. V5 also needs a *fresh* live match id at run time
(re-run V3 — matches end in ~40 min).

