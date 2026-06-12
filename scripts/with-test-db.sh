#!/usr/bin/env bash
# Spin up an ephemeral PostgreSQL cluster, export POSTGRESQL_URL pointing at it,
# run the given command, then tear the cluster down. Used by `make test-go` so
# the database-backed tests run against a real PostgreSQL without touching any
# existing instance or requiring setup.
#
# If POSTGRESQL_URL is already set, the command runs against that instead and no
# cluster is created. initdb/pg_ctl must be on PATH (override with PG_BINDIR,
# e.g. on Debian: PG_BINDIR=/usr/lib/postgresql/18/bin).
set -euo pipefail

if [[ $# -eq 0 ]]; then
  echo "usage: with-test-db.sh <command> [args...]" >&2
  exit 2
fi

if [[ -n "${POSTGRESQL_URL:-}" ]]; then
  exec "$@"
fi

PG_BINDIR="${PG_BINDIR:-}"
INITDB="${PG_BINDIR:+$PG_BINDIR/}initdb"
PG_CTL="${PG_BINDIR:+$PG_BINDIR/}pg_ctl"

DATADIR="$(mktemp -d)"
SOCKDIR="$(mktemp -d)"

cleanup() {
  "$PG_CTL" -D "$DATADIR" -m immediate stop >/dev/null 2>&1 || true
  rm -rf "$DATADIR" "$SOCKDIR"
}
trap cleanup EXIT

# --auth=trust: local-only test cluster; -N: skip fsync for speed.
"$INITDB" -D "$DATADIR" -U postgres --auth=trust -N >/dev/null
# Unix socket only (listen_addresses=''), isolated in SOCKDIR so it never
# collides with a system PostgreSQL on the same port.
"$PG_CTL" -D "$DATADIR" -o "-k $SOCKDIR -c listen_addresses=''" -w start >/dev/null

export POSTGRESQL_URL="postgres://postgres@/postgres?host=$SOCKDIR"
"$@"
