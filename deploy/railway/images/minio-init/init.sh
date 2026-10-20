#!/usr/bin/env bash
# One-shot, idempotent: the buckets and the sandbox lifecycle rule (the same as the minio-init service of deploy/compose/compose.yaml).
set -euo pipefail

: "${S3_ENDPOINT:?}" "${S3_ACCESS_KEY:?}" "${S3_SECRET_KEY:?}"
export MC_CONFIG_DIR=/tmp/mc
until mc alias set local "$S3_ENDPOINT" "$S3_ACCESS_KEY" "$S3_SECRET_KEY" >/dev/null 2>&1; do echo "waiting for $S3_ENDPOINT"; sleep 3; done
for b in invoices assets; do
  mc mb --ignore-existing "local/$b"
  rules=$(mc ilm rule ls --json "local/$b" 2>/dev/null || true)
  if [[ $rules != *environment* ]]; then
    mc ilm rule add --expire-days 30 --tags 'environment=sandbox' "local/$b"
  fi
done
echo buckets ready
