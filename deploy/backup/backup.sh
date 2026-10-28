#!/bin/sh
# Backs up every Payrudder database with pg_dump (custom format, compressed), keeps the newest BACKUP_KEEP of each, optionally copies the files to S3-compatible
# storage and reports to a Prometheus Pushgateway when it succeeded, so the BackupStale alert can notice a backup that stopped. docs/runbooks/backup-restore.md.
#
# Environment: PGHOST PGPORT PGUSER PGPASSWORD (an admin role that can read every database), BACKUP_DIR (default /backups), BACKUP_KEEP (default 7 dumps per
# database), BACKUP_DATABASES (default: every *_db), BACKUP_S3_URL (optional, "s3://bucket/prefix"; needs the aws CLI and its AWS_* variables, plus
# AWS_ENDPOINT_URL for MinIO), PUSHGATEWAY_URL (optional, e.g. http://pushgateway:9091).
set -eu

dir="${BACKUP_DIR:-/backups}"
keep="${BACKUP_KEEP:-7}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$dir"

dbs="${BACKUP_DATABASES:-$(psql -XAt -d postgres -c "SELECT datname FROM pg_database WHERE datname LIKE '%\\_db' ORDER BY 1")}"
[ -n "$dbs" ] || { echo "backup: no database to back up" >&2; exit 1; }

failed=0
for db in $dbs; do
  file="$dir/$db-$stamp.dump"
  if pg_dump --format=custom --compress=6 --no-owner --file="$file.partial" "$db"; then
    mv "$file.partial" "$file"
    # A dump that cannot be listed is not a backup.
    if pg_restore --list "$file" >/dev/null; then
      echo "backup: $db -> $file ($(wc -c <"$file") bytes)"
    else
      echo "backup: $db dump is unreadable, removed" >&2
      rm -f "$file"
      failed=1
      continue
    fi
    if [ -n "${BACKUP_S3_URL:-}" ]; then
      aws s3 cp --only-show-errors "$file" "${BACKUP_S3_URL%/}/$db/$(basename "$file")" || { echo "backup: upload of $file failed" >&2; failed=1; }
    fi
  else
    echo "backup: pg_dump of $db failed" >&2
    rm -f "$file.partial"
    failed=1
    continue
  fi
  # Retention: the newest $keep dumps of this database stay.
  ls -1t "$dir"/"$db"-*.dump 2>/dev/null | tail -n +"$((keep + 1))" | while read -r old; do rm -f "$old"; done
done

if [ -n "${PUSHGATEWAY_URL:-}" ]; then
  now="$(date +%s)"
  {
    echo "# TYPE payrudder_backup_last_run_timestamp_seconds gauge"
    echo "payrudder_backup_last_run_timestamp_seconds $now"
    if [ "$failed" = 0 ]; then
      echo "# TYPE payrudder_backup_last_success_timestamp_seconds gauge"
      echo "payrudder_backup_last_success_timestamp_seconds $now"
    fi
    echo "# TYPE payrudder_backup_failed gauge"
    echo "payrudder_backup_failed $failed"
  } | curl -fsS --max-time 10 --data-binary @- "${PUSHGATEWAY_URL%/}/metrics/job/payrudder_backup" || echo "backup: could not report to the pushgateway" >&2
fi

exit "$failed"
