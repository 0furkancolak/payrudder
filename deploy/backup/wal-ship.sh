#!/bin/sh
# Moves the archived WAL from the spool (wal-archive.sh) to S3-compatible storage, oldest first, and reports to the Pushgateway so the WalArchiveStale alert notices when
# nothing arrives any more: payrudder_wal_last_shipped_timestamp_seconds moves only when a file was shipped (the stack writes a marker every few minutes and Postgres switches
# segment after archive_timeout, so a healthy system ships at least every ~6 minutes). A file leaves the spool only after its upload succeeded.
# Environment: WAL_S3_URL ("s3://bucket/prefix"; needs the aws CLI, its AWS_* variables and AWS_ENDPOINT_URL for MinIO), WAL_SPOOL, PUSHGATEWAY_URL (optional, needs curl).
set -eu
: "${WAL_S3_URL:?wal-ship: set WAL_S3_URL}"
spool="${WAL_SPOOL:-/var/run/postgresql/wal-spool}"
shipped=0
failed=0
# A name ending in .gz.tmp is a file wal-archive.sh is still writing.
for f in $(ls -1 "$spool" 2>/dev/null | grep '\.gz$' | sort); do
  if aws s3 cp --only-show-errors "$spool/$f" "${WAL_S3_URL%/}/wal/$f"; then
    rm -f "$spool/$f"
    shipped=$((shipped + 1))
  else
    echo "wal-ship: upload of $f failed" >&2
    failed=1
    break
  fi
done

left="$(ls -1 "$spool" 2>/dev/null | grep -c '\.gz$' || true)"
oldest=0
if [ "$left" -gt 0 ]; then
  first="$(ls -1 "$spool" | grep '\.gz$' | sort | head -1)"
  oldest=$(($(date +%s) - $(stat -c %Y "$spool/$first")))
fi
[ "$shipped" -eq 0 ] || echo "wal-ship: $shipped file(s) shipped, $left left"

if [ -n "${PUSHGATEWAY_URL:-}" ]; then
  now="$(date +%s)"
  {
    echo "# TYPE payrudder_wal_ship_last_run_timestamp_seconds gauge"
    echo "payrudder_wal_ship_last_run_timestamp_seconds $now"
    if [ "$shipped" -gt 0 ]; then
      echo "# TYPE payrudder_wal_last_shipped_timestamp_seconds gauge"
      echo "payrudder_wal_last_shipped_timestamp_seconds $now"
    fi
    echo "# TYPE payrudder_wal_spool_files gauge"
    echo "payrudder_wal_spool_files $left"
    echo "# TYPE payrudder_wal_oldest_unshipped_age_seconds gauge"
    echo "payrudder_wal_oldest_unshipped_age_seconds $oldest"
  } | curl -fsS --max-time 10 --data-binary @- "${PUSHGATEWAY_URL%/}/metrics/job/payrudder_wal" || echo "wal-ship: could not report to the pushgateway" >&2
fi
exit "$failed"
