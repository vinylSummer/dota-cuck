#!/usr/bin/env bash
# V2 — install Dota 2 (app 570) via steamcmd into the steam-data location.
#
# The root fs is nearly full; install into the ZFS dataset /fard/steam (≈129G free),
# which is the intended steam-data volume location for deployment. steamcmd needs its
# own Steam Guard (a separate sentry from the python-steam session — a V4 data point):
# when it prompts, write the code to ~/.dota-validation.guard and this runner feeds it
# to steamcmd's stdin.
#
#   scripts/validation/v2_install.sh
set -euo pipefail
set -a; . ~/.dota-validation.env; set +a

INSTALL=${STEAM_INSTALL_DIR:-/fard/steam/dota}
GUARD_FILE=~/.dota-validation.guard
LOG=/tmp/v2.log
FIFO=/tmp/steamcmd.fifo

mkdir -p "$INSTALL"
rm -f "$FIFO" "$GUARD_FILE"; mkfifo "$FIFO"
: > "$LOG"

# Open the FIFO read+write so this never blocks on a missing peer and steamcmd's stdin
# never EOFs while it waits for a code.
exec 3<>"$FIFO"

steamcmd \
    +force_install_dir "$INSTALL" \
    +login "$STEAM_USER" "$STEAM_PASS" \
    +app_update 570 validate \
    +quit < "$FIFO" > "$LOG" 2>&1 &
SC=$!
echo "steamcmd pid $SC, install dir $INSTALL, log $LOG"

# Feed any guard code the operator drops into GUARD_FILE through to steamcmd's stdin.
while kill -0 "$SC" 2>/dev/null; do
    if [ -f "$GUARD_FILE" ]; then
        code=$(cat "$GUARD_FILE"); rm -f "$GUARD_FILE"
        printf '%s\n' "$code" >&3
        echo "fed guard code to steamcmd"
    fi
    sleep 1
done

wait "$SC"; rc=$?
exec 3>&-
echo "steamcmd exit $rc"
echo "=== installed size ==="; du -sh "$INSTALL" 2>/dev/null || true
exit $rc
