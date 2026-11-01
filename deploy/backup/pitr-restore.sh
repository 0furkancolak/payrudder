#!/bin/sh
# Prepares a Postgres data directory that recovers to a point in time: pitr-restore.sh <target> <data dir>
#   target    'latest' (replay everything archived), or a UTC time 'YYYY-MM-DD HH:MM:SS[.ffffff]' (optionally followed by +00 or UTC); other time zones are refused,
#             because a restore to the wrong hour is the likeliest mistake of a night with an incident
#   data dir  must not hold a cluster yet (an empty volume mounted where the server will find it)
# It downloads the newest base backup that ended at or before the target (pitr-basebackup.sh) and the archived WAL from its start (wal-ship.sh), unpacks them, and writes
# the recovery settings; it does not start Postgres. Start the server (same major version) on the directory: it replays the WAL up to the target and, with
# recovery_target_action = promote, opens read-write there. The target must lie before the end of the archive, or recovery stops with an error instead of guessing:
# switch the segment (SELECT pg_switch_wal()) and ship it first when restoring to "just now". The restored server must not archive into the same place: start it
# without archive_mode. The WAL is downloaded whole; for a large archive fetch it on demand with a restore_command of your own instead.
# Environment: WAL_S3_URL ("s3://bucket/prefix"; aws CLI, AWS_* variables, AWS_ENDPOINT_URL for MinIO). Run as root it gives the directory to the postgres user.
set -eu
target="${1:?usage: pitr-restore.sh <latest|'YYYY-MM-DD HH:MM:SS'> <data dir>}"
data="${2:?usage: pitr-restore.sh <latest|'YYYY-MM-DD HH:MM:SS'> <data dir>}"
: "${WAL_S3_URL:?pitr-restore: set WAL_S3_URL}"
die() { echo "pitr-restore: $1" >&2; exit 1; }
[ ! -e "$data/PG_VERSION" ] || die "$data already holds a cluster"

if [ "$target" = latest ]; then
  limit=99999999T999999Z
else
  echo "$target" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?( ?\+00(:00)?| ?UTC)?$' || die "the target is 'latest' or a UTC time like '2026-11-01 12:30:00'"
  limit="$(echo "$target" | cut -c1-19 | sed 's/[-: ]//g; s/^\(........\)/\1T/; s/$/Z/')"
  target_time="$(echo "$target" | sed -E 's/ ?(\+00(:00)?|UTC)$//') UTC"
fi

# The newest complete base backup that ended at or before the target.
base="${WAL_S3_URL%/}/base"
stamp=""
for s in $(aws s3 ls "$base/" | awk -v l="$limit" '$1 == "PRE" {s = $2; sub("/", "", s); if (s <= l) print s}' | sort -r); do
  if [ -n "$(aws s3 ls "$base/$s/start_wal")" ]; then stamp="$s"; break; fi
done
[ -n "$stamp" ] || die "no complete base backup ended at or before $target (in $base/)"
echo "pitr-restore: base backup $stamp, target $target"

mkdir -p "$data"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
aws s3 cp --only-show-errors "$base/$stamp/base.tar.gz" "$tmp/base.tar.gz"
aws s3 cp --only-show-errors "$base/$stamp/pg_wal.tar.gz" "$tmp/pg_wal.tar.gz"
start="$(aws s3 cp --only-show-errors "$base/$stamp/start_wal" -)"
tar -xzf "$tmp/base.tar.gz" -C "$data"
mkdir -p "$data/pg_wal"
tar -xzf "$tmp/pg_wal.tar.gz" -C "$data/pg_wal"

# The archive from the base backup's first WAL file on, plus every timeline history file.
mkdir -p "$data/pitr_wal"
set -- --exclude '*'
for n in $(aws s3 ls "${WAL_S3_URL%/}/wal/" | awk -v s="$start.gz" '$4 >= s || $4 ~ /\.history\.gz$/ {print $4}'); do set -- "$@" --include "$n"; done
aws s3 sync --only-show-errors "${WAL_S3_URL%/}/wal/" "$data/pitr_wal" "$@"
if ls "$data"/pitr_wal/*.gz >/dev/null 2>&1; then gunzip -f "$data"/pitr_wal/*.gz; fi

# A server that replays WAL as a hot standby needs at least the settings of the one that wrote it (it refuses to start otherwise): they are in the control file.
{
  echo "# pitr-restore.sh"
  pg_controldata "$data" | awk -F: '
    /^max_connections setting/ {print "max_connections = " $2+0}
    /^max_worker_processes setting/ {print "max_worker_processes = " $2+0}
    /^max_wal_senders setting/ {print "max_wal_senders = " $2+0}
    /^max_prepared_xacts setting/ {print "max_prepared_transactions = " $2+0}
    /^max_locks_per_xact setting/ {print "max_locks_per_transaction = " $2+0}'
  echo "restore_command = 'cp pitr_wal/%f \"%p\"'"
  [ "$target" = latest ] || echo "recovery_target_time = '$target_time'"
  echo "recovery_target_action = 'promote'"
} >>"$data/postgresql.auto.conf"
touch "$data/recovery.signal"
chmod 700 "$data"
if [ "$(id -u)" = 0 ]; then chown -R postgres:postgres "$data"; fi
echo "pitr-restore: $data is ready; start Postgres on it (without archive_mode) and wait for it to leave recovery"
