#!/bin/sh
# The sidecar of the point-in-time stack, one process forever: ships the spooled WAL to storage every WAL_SHIP_INTERVAL seconds (wal-ship.sh), writes a marker row into
# the postgres database every MARKER_INTERVAL seconds (so there is always activity to archive and a recovery point to look for after a restore:
# SELECT max(written_at) FROM payrudder_pitr_markers), and takes a base backup when the newest is older than BASEBACKUP_INTERVAL (pitr-basebackup.sh, also right at start).
# RPO: archive_timeout (300 s, set where Postgres starts) + WAL_SHIP_INTERVAL + the upload; the WalArchiveStale alert fires when nothing was shipped for 15 minutes.
# Environment: as wal-ship.sh and pitr-basebackup.sh, plus PGHOST PGUSER (PGPASSWORD) to write the marker.
set -u
here="$(dirname "$0")"
ship="${WAL_SHIP_INTERVAL:-30}"
marker="${MARKER_INTERVAL:-300}"
export BASEBACKUP_MIN_AGE="${BASEBACKUP_INTERVAL:-86400}"
last_marker=0
last_base=0
trap 'exit 0' TERM INT
while true; do
  now="$(date +%s)"
  if [ $((now - last_marker)) -ge "$marker" ]; then
    psql -X -q -v ON_ERROR_STOP=1 -d postgres -c "CREATE TABLE IF NOT EXISTS payrudder_pitr_markers (id bigserial PRIMARY KEY, written_at timestamptz NOT NULL DEFAULT now(), note text)" \
      -c "INSERT INTO payrudder_pitr_markers (note) VALUES ('agent')" && last_marker="$now" || echo "pitr-agent: could not write the marker" >&2
  fi
  # The base backup script lists the storage to see how old the newest one is; every ten minutes is often enough.
  if [ $((now - last_base)) -ge 600 ]; then
    sh "$here/pitr-basebackup.sh" || true
    last_base="$now"
  fi
  sh "$here/wal-ship.sh" || true
  sleep "$ship" &
  wait $!
done
