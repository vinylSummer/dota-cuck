# Known Risks — Validate Manually Before Implementing

These must be confirmed on the actual server before writing the worker spectate path.

## ⚠️ CRITICAL: Dual Steam session problem

python-steam (GC queries / friends) and the GUI Steam client cannot both be logged into
the same account simultaneously — Steam terminates one session. Intended handoff:

```
python-steam login → GC query (match ID for target_steam_id) → python-steam logout
  → GUI Steam launch + login → Dota launch → spectate match_id
```

This is why the warm friends session is **dropped while spectating** and re-warmed after.

**Risk:** GUI Steam login may re-trigger Steam Guard even after python-steam established
device trust (separate sentry files). Validate manually. If both need separate Steam Guard
confirmations, the two-prompt UX must be handled in the session state machine and surfaced
to the user.

**Mitigation to test:** pre-populate the GUI Steam sentry from the python-steam session, or
accept that first login needs two Steam Guard confirmations and later logins reuse saved
sentries.

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
