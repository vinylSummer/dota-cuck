# Known Risks — Validation Status

These were the risks to confirm on the real server before writing the worker spectate path.
Results live in [validation-results.md](validation-results.md); current status is inline below.

## ✅ RESOLVED: GUI Steam login + dual session

python-steam (match-ID resolution / friends) and the GUI Steam client cannot both be logged into
the same account simultaneously — Steam terminates one session. The original worry was that the
GUI Steam client is a separate session that **cannot ingest the refresh token**, so its first
login would re-trigger Steam Guard with no way to complete it headlessly.

**Resolved (V4):** the GUI client's token store is encrypted with an unpublished scheme, so a
refresh token genuinely can't be seeded — but the modern Steam login UI (incl. its **QR code**)
*does* render headless once the `docker-steam-headless` GPU/desktop stack is replicated (Xfce4 +
full in-image NVIDIA driver + fake-monitor xorg.conf + `--shm-size=2g` + system dbus). So the GUI
client does a **one-time interactive QR login**, and its persisted token then **auto-logs-in
silently** on every later start (proven: fresh container, no VNC, no creds → `Logged On`).

Design consequence: there are **two auth artifacts** — the user's python-steam refresh token
(friends + `WatchableGameID`) and the GUI client's own **per-user** one-time login (persisted on
the `steam-data` volume). For V1 the spectator *is* the user's account, so no python-steam↔GUI
handoff is exercised. (Match-ID resolution is rich presence, **not** a GC query — V3.)

## ⚠️ Dota spectate console command (partially open)

`steam -applaunch 570` can't see a `force_install_dir` install; Dota launches via the install's
sniper wrapper (`run-in-sniper`) with the GUI client for auth — **validated: it authenticates and
renders the menu** (V5). The remaining open item is **driving the in-game console**: `xdotool`
(XTEST) events don't reach Source 2, so a virtual **uinput** device (`/dev/uinput` + `ydotool`) is
needed before confirming the join command (`dota_spectate_game <match_id>`) + camera follow.

## ✅ RESOLVED: Headless Xorg inside Docker with NVIDIA

Validated (V1): `Xorg :99` on the RTX 3090 (`BusID PCI:11:0:0`) with hardware GLX inside the
container; NVENC (V6) and full Dota Vulkan rendering (V5) also confirmed.

## ✅ RESOLVED: Steam + Dota installation in Docker

Validated (V2): ~70GB Dota install via `steamcmd +force_install_dir /fard/steam/dota` onto the
persistent ZFS dataset; resumable updates. See [deployment.md](deployment.md).
