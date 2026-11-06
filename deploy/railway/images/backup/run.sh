#!/bin/sh
# Every BACKUP_INTERVAL_SECONDS (a day): dump every database to s3://$BACKUP_BUCKET/postgres/ (deploy/backup/backup.sh; the bucket's lifecycle rule
# expires dumps after 30 days), prove the newest dumps restore every VERIFY_EVERY_RUNS runs, the first run included (verify.sh), and copy the object
# buckets to s3://$BACKUP_BUCKET/objects/<bucket>/ (a delete in the source is not copied). Each step reports to the Pushgateway, which the backup alerts read.
# Environment: PG* (the postgres superuser), BACKUP_BUCKET, OBJ_BUCKETS (space separated), AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_ENDPOINT_URL
# (an R2 token that writes the backup bucket and reads the object buckets), PUSHGATEWAY_URL.
set -u
: "${BACKUP_BUCKET:?}" "${AWS_ENDPOINT_URL:?}" "${AWS_ACCESS_KEY_ID:?}" "${AWS_SECRET_ACCESS_KEY:?}"
export AWS_DEFAULT_REGION=${AWS_DEFAULT_REGION:-auto} BACKUP_S3_URL="s3://$BACKUP_BUCKET/postgres" BACKUP_DIR=${BACKUP_DIR:-/tmp/backups} BACKUP_KEEP=${BACKUP_KEEP:-2}

objects() {
  failed=0
  for b in ${OBJ_BUCKETS:-}; do
    aws s3 sync --only-show-errors "s3://$b" "s3://$BACKUP_BUCKET/objects/$b" || { echo "objects: copy of $b failed" >&2; failed=1; }
  done
  [ -n "${PUSHGATEWAY_URL:-}" ] || return "$failed"
  now=$(date +%s)
  {
    echo "payrudder_objects_last_run_timestamp_seconds $now"
    [ "$failed" = 0 ] && echo "payrudder_objects_last_success_timestamp_seconds $now"
    echo "payrudder_objects_backup_failed $failed"
  } | curl -fsS --max-time 10 --data-binary @- "${PUSHGATEWAY_URL%/}/metrics/job/payrudder_objects" || echo "objects: could not report to the pushgateway" >&2
  return "$failed"
}

n=0
while true; do
  sh /scripts/backup.sh || true
  n=$((n + 1))
  [ $((n % ${VERIFY_EVERY_RUNS:-7})) -eq 1 ] && { sh /scripts/verify.sh || true; }
  objects || true
  sleep "${BACKUP_INTERVAL_SECONDS:-86400}"
done
