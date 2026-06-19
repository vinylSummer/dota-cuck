# Known Risks — Validate Manually Before Implementing

These must be confirmed on the actual server before writing the worker spectate path.

## ⚠️ CRITICAL: Dual Steam session problem

python-steam (match-ID resolution / friends) and the GUI Steam client cannot both be logged into
the same account simultaneously — Steam terminates one session. Intended handoff:

```
python-steam login → resolve match ID (rich-presence WatchableGameID for target_steam_id)
  → python-steam logout → GUI Steam launch + login → Dota launch → spectate match_id
```

(Match-ID resolution is the warm session's rich presence, **not** a Game Coordinator query — see
V3 in [validation-results.md](validation-results.md).)

This is why the warm friends session is **dropped while spectating** and re-warmed after.

**Risk:** GUI Steam login is a separate Steam session from the python-steam one and can't
ingest the refresh token, so its first login may re-trigger Steam Guard even though the
python-steam cold login is already token-based (zero-interaction). Validate manually. If GUI
Steam needs its own Steam Guard confirmation, the prompt UX must be handled in the session
state machine and surfaced to the user.

**Mitigation to test:** accept that the GUI Steam first login needs a Steam Guard confirmation
and that later logins reuse its own persisted device trust on the `steam-data` volume.

## ⚠️ Dota spectate console command

The exact console sequence to join a live match by ID must be confirmed. Candidate:
`dota_spectate_game <match_id>` — or it may require a lobby/server connect flow. Validate
headlessly with a known live match ID before implementing the automation layer.

## ⚠️ Headless Xorg inside Docker with NVIDIA

Xorg must run on the GPU without a physical display. Requires `xorg.conf` with the correct
RTX 3090 `BusID`, `nvidia-container-toolkit` on the host, and `DISPLAY=:99` in the
container. Validate a GPU-accelerated process (`glxinfo`, `nvidia-smi`) works inside the
worker container before writing automation.

## ⚠️ Steam + Dota installation in Docker

See [deployment.md](deployment.md) — ~70GB install via steamcmd into a named volume;
decide the update strategy up front.
