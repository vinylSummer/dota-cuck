# Validation Results

Live validation of the high-risk worker spectate path on the target server **`wolf-den`**.
Each probe lives in `scripts/validation/`. Prior outcomes (V1–V3, V6) are summarized from the
handoff (`plan.md` §3); V4/V5 are filled in when their probes run.

## Environment (wolf-den)

- Debian 13 (trixie, kernel 6.12.90), RTX 3090, NVIDIA driver 610.43.02, Docker 29.5.2 with the
  `nvidia` runtime + CDI. Host `sudo` needs a password → all GPU/Steam/Dota work runs in containers.
- Dota installed at `/fard/steam/dota` (root fs ~98% full; Steam lives on the ZFS dataset
  `/fard/steam`). GUI Steam config + sentries persist under `/fard/steam/steamhome` (probe default).

## Tooling notes (probe gotchas — save the next runner the debugging)

- **X readiness: use `xset q`, not `xdpyinfo`.** `xdpyinfo` is in `x11-utils`, which is NOT in the
  validation images (they install `x11-xserver-utils` → `xset`/`xrandr`, and `glxinfo` via
  `mesa-utils`). Polling `xdpyinfo` silently fails every iteration even when X is up. (V1's script
  only "passed" because it runs `glxinfo` regardless of the poll.) The V4/V5 probes use `xset q`.
- **`steam` / `steamcmd` live in `/usr/games`**, which is not on the default non-login `PATH`.
  `Dockerfile.steam` adds `ENV PATH=/usr/games:$PATH` so they resolve inside `bash -c`.
- **`steam-installer` / `steamcmd` gate on a debconf license prompt** — `Dockerfile.steam`
  pre-accepts it (`debconf-set-selections`) so the non-interactive build doesn't hang.

## V1 — Headless Xorg + NVIDIA GLX — **PASS**

`v1_headless_gpu.sh`. Container renders on the 3090 (`direct rendering: Yes`, NVIDIA 610.43.02);
`worker/xorg/xorg.conf` BusID `PCI:11:0:0` correct. Toolkit injects the driver at runtime.

## V2 — Dota install via steamcmd — **PASS**

`v2_install.sh`. Installs into `/fard/steam/dota` (~72 GB logical), resumable, survives rebuilds.
**steamcmd's first login used a mobile-confirmation _tap_, not a typed code** — flags the V4 risk.

## V3 — Live match-ID resolution — **PASS**

`v3_gc_matchid.py`. The match id is the rich-presence **`WatchableGameID`**, not a GC query;
present/>0 only for live, watchable, public matches. Request with `state_flags=0x35F | 0x200`
then poll (RP is not instant). No `dota2` dependency. Now wired into `worker/steam_client.py`
(`resolve_match_id`) + `worker/agent.py` (Task B).

## V6 — x11grab → hevc_nvenc → SRT → mediamtx — **PASS**

`v6_nvenc.sh`, `v6_srt_mediamtx.sh`. Real-time 60fps 720p H.265 ~2.3 Mbps. SRT **streamid must be
`publish:live/match`**. `mediamtx/mediamtx.yml` ready.

---

## V4 — Dual-session handoff + GUI-Steam Steam Guard — **PARTIAL** (phase 1 PASS)

Probe: `v4_dual_session.sh` (+ `v4_steam_phase.py`). Needs live creds (`~/.dota-validation.env`).
Run while you can complete a one-time mobile confirmation on your phone. Container
`HOME=/fard/steam/steamhome` (sentries persist on the ZFS dataset).

- [x] **Phase 1 PASS (2026-06-17)** — python-steam logged in as STEAM_USER (`vinyl summer`,
      `76561198179568701`), saw 194 friends, and logged out cleanly (`PYTHON_STEAM_LOGGED_OUT`),
      vacating the account for the GUI client. Needed one mobile Steam Guard code (no sentry yet;
      the run wrote the python-steam sentry to `/fard/steam/steamhome/.dota-validation-sentry`).
- [ ] **GUI-Steam first login**: did Steam Guard / a mobile-confirmation **tap** fire? Tap
      (uncompletable headless, needs a one-time human) or a typed code? — _not yet observed_
- [ ] After completing the first login, is the **next** GUI-Steam login **silent** (sentry trusted)?
- [ ] Exact GUI-Steam **sentry mechanism + file location** (for the `steam-data` volume).
- [ ] Only **one** session on the account at a time — no "logged in elsewhere" kick after handoff?
- [ ] Implication for route A: if a guard genuinely fires per-login, the session SM must surface a
      second session-scoped `SteamGuardRequired` (empty `request_id`).

Screenshots: `/fard/steam/steamhome/v4-shots/`. _Phase 2 findings:_ _(fill in)_

---

## V5 — Dota spectate console command + camera follow — **PENDING**

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
