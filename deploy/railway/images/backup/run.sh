#!/bin/sh
# Every BACKUP_INTERVAL_SECONDS (a day): dump every database to s3://$BACKUP_BUCKET/postgres/ (deploy/backup/backup.sh; the bucket's lifecycle rule
# expires dumps after 30 days) and prove the newest dumps restore every VERIFY_EVERY_RUNS runs, the first run included (verify.sh). Both report to
# the Pushgateway, which the backup alerts read. The object buckets are not backed up.
# Environment: PG* (the postgres superuser), BACKUP_BUCKET, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_ENDPOINT_URL (an R2 token that writes the
# backup bucket), PUSHGATEWAY_URL.
set -u
: "${BACKUP_BUCKET:?}" "${AWS_ENDPOINT_URL:?}" "${AWS_ACCESS_KEY_ID:?}" "${AWS_SECRET_ACCESS_KEY:?}"
export AWS_DEFAULT_REGION=${AWS_DEFAULT_REGION:-auto} BACKUP_S3_URL="s3://$BACKUP_BUCKET/postgres" BACKUP_DIR=${BACKUP_DIR:-/tmp/backups} BACKUP_KEEP=${BACKUP_KEEP:-2}

n=0
while true; do
  sh /scripts/backup.sh || true
  n=$((n + 1))
  [ $((n % ${VERIFY_EVERY_RUNS:-7})) -eq 1 ] && { sh /scripts/verify.sh || true; }
  sleep "${BACKUP_INTERVAL_SECONDS:-86400}"
done
