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

## ⚠️ Dota spectate initiation — mechanism corrected; GUI-automation (open)

`steam -applaunch 570` can't see a `force_install_dir` install; Dota launches via the install's
sniper wrapper (`run-in-sniper`) with the GUI client for auth — **validated: it authenticates and
renders the menu** (V5). **Input injection is now solved** too: a libinput-bound **uinput** device
(keyboard + absolute mouse) delivers real X input that Source 2 accepts (XTEST is ignored).

**Corrected (2026-06-24):** the assumed console join command **`dota_spectate_game <match_id>` does
not exist** (verified against the wiki and this build's binaries) — and **no console command joins a
live match by id at all**. Live spectating is **GC-mediated through the GUI** (the Watch tab is
tournaments/replays only). **Decision:** initiate a friend spectate by **automating the GUI**
(friends panel → right-click friend in a live match → Spectate) with the uinput mouse; the native
client does the GC handshake/connect/render. This is **team-vision-only** (Dota Plus) — accepted for
V1. GC automation (python-dota2) was rejected: it can't render, needs a second GC session the account
can't grant, and was already proven not to connect (V3). The open item is the exact GUI click path +
in-session camera commands (the real ones: `dota_spectator_mode`, `spec_player`, …). See
[validation-results.md](validation-results.md) V5.

## ✅ RESOLVED: Headless Xorg inside Docker with NVIDIA

Validated (V1): `Xorg :99` on the RTX 3090 (`BusID PCI:11:0:0`) with hardware GLX inside the
container; NVENC (V6) and full Dota Vulkan rendering (V5) also confirmed.

## ✅ RESOLVED: Steam + Dota installation in Docker

Validated (V2): ~70GB Dota install via `steamcmd +force_install_dir /fard/steam/dota` onto the
persistent ZFS dataset; resumable updates. See [deployment.md](deployment.md).
