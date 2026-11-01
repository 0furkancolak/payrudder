#!/bin/sh
# Takes a base backup of the whole Postgres cluster into S3-compatible storage: the starting point of a point-in-time restore (pitr-restore.sh), which then replays the
# WAL that wal-archive.sh and wal-ship.sh keep. It needs a role with the REPLICATION attribute (a superuser has it) and either a replication entry in pg_hba.conf for the
# host it connects from or the server's local socket (the compose stack shares the socket with the sidecar). A cluster with extra tablespaces is not handled.
# A base backup is s3://.../base/<UTC time it ended>/{base.tar.gz,pg_wal.tar.gz,start_wal}; start_wal is written last, so one without it is unfinished and never used.
# Removing old ones is the bucket's lifecycle rule (docs/runbooks/backup-restore.md), not this script.
# Environment: PGHOST PGPORT PGUSER PGPASSWORD, WAL_S3_URL ("s3://bucket/prefix"; needs the aws CLI, its AWS_* variables and AWS_ENDPOINT_URL for MinIO),
# BASEBACKUP_MIN_AGE (seconds: do nothing when the newest base backup is younger, default 0), PUSHGATEWAY_URL (optional, needs curl).
set -eu
: "${WAL_S3_URL:?pitr-basebackup: set WAL_S3_URL}"
base="${WAL_S3_URL%/}/base"
min_age="${BASEBACKUP_MIN_AGE:-0}"

report() { # 0 = the base backup is in storage, 1 = it failed
  [ -n "${PUSHGATEWAY_URL:-}" ] || return 0
  {
    if [ "$1" = 0 ]; then
      echo "# TYPE payrudder_pitr_basebackup_last_success_timestamp_seconds gauge"
      echo "payrudder_pitr_basebackup_last_success_timestamp_seconds $(date +%s)"
    fi
    echo "# TYPE payrudder_pitr_basebackup_failed gauge"
    echo "payrudder_pitr_basebackup_failed $1"
  } | curl -fsS --max-time 10 --data-binary @- "${PUSHGATEWAY_URL%/}/metrics/job/payrudder_pitr_basebackup" || echo "pitr-basebackup: could not report to the pushgateway" >&2
}
fail() { echo "pitr-basebackup: $1" >&2; report 1; exit 1; }

if [ "$min_age" -gt 0 ]; then
  newest="$(aws s3 ls "$base/" | awk '$1 == "PRE" {print $2}' | tr -d / | sort | tail -1)"
  if [ -n "$newest" ]; then
    # busybox date: -D is the format of -d.
    age=$(($(date +%s) - $(TZ=UTC date -D %Y%m%dT%H%M%SZ -d "$newest" +%s)))
    if [ "$age" -lt "$min_age" ]; then
      echo "pitr-basebackup: the newest base backup ($newest) is ${age}s old, nothing to do"
      exit 0
    fi
  fi
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
pg_basebackup --pgdata="$tmp" --format=tar --gzip --wal-method=stream --checkpoint=fast --label=payrudder-pitr || fail "pg_basebackup failed"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
# The WAL file the base backup starts at: replay needs the archive from there on.
start="$(tar -xzOf "$tmp/base.tar.gz" backup_label | sed -n 's/^START WAL LOCATION: .*(file \([0-9A-F]*\))$/\1/p')"
[ -n "$start" ] || fail "no start WAL in the backup label"
for f in "$tmp"/*.tar.gz; do
  aws s3 cp --only-show-errors "$f" "$base/$stamp/$(basename "$f")" || fail "upload of $(basename "$f") failed"
done
printf '%s\n' "$start" | aws s3 cp --only-show-errors - "$base/$stamp/start_wal" || fail "upload of start_wal failed"
echo "pitr-basebackup: $base/$stamp (starts at WAL $start)"
report 0
