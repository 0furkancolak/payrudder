#!/usr/bin/env bash
# Copies the object buckets (invoice PDFs, assets) to a second place with mc, and back: object-backup.sh mirror | restore | verify. Written for the minio-client image of the compose
# file, which has bash, date and mc and nothing else (no curl, no sed): the report to the Pushgateway is plain HTTP over /dev/tcp.
#
#   mirror   copies every object of each bucket in OBJ_BUCKETS to <OBJ_TARGET_BUCKET>/<OBJ_TARGET_PREFIX>/<bucket>/ on the target, overwriting a changed object, then compares both
#            sides (mc diff): an object that is missing on the target or differs is a failure. A DELETE in the source does NOT reach the target (a bucket emptied by a
#            mistake or an attack must not empty its backup too): set OBJ_MIRROR_REMOVE=1 to propagate deletes, and give the target bucket versioning and a lifecycle rule instead.
#   restore  copies the target back into the source buckets. It overwrites and never deletes; what was lost is back, what was added since stays.
#   verify   only compares (the comparison mirror ends with): is every object of the source on the target, and the same?
#
# Environment: OBJ_SOURCE_URL OBJ_SOURCE_USER OBJ_SOURCE_PASSWORD (the MinIO or S3 that serves the platform); OBJ_TARGET_URL/_USER/_PASSWORD (default: the source, which only
# protects against a bucket being emptied, not against losing the volume: in production point it at another account or region); OBJ_BUCKETS (default "invoices assets");
# OBJ_TARGET_BUCKET (default backups); OBJ_TARGET_PREFIX (default objects); PUSHGATEWAY_HOST_PORT (optional, "pushgateway:9091": the run is reported as
# payrudder_objects_*). docs/runbooks/stateful-components.md.
set -euo pipefail
mode="${1:?usage: object-backup.sh mirror|restore|verify}"
[[ "$mode" == mirror || "$mode" == restore || "$mode" == verify ]] || { echo "object-backup: mode is mirror, restore or verify" >&2; exit 2; }
export MC_CONFIG_DIR="${MC_CONFIG_DIR:-/tmp/mc}"
buckets="${OBJ_BUCKETS:-invoices assets}"
tb="${OBJ_TARGET_BUCKET:-backups}"
prefix="${OBJ_TARGET_PREFIX:-objects}"

mc alias set src "${OBJ_SOURCE_URL:?OBJ_SOURCE_URL}" "${OBJ_SOURCE_USER:?OBJ_SOURCE_USER}" "${OBJ_SOURCE_PASSWORD:?OBJ_SOURCE_PASSWORD}" >/dev/null
mc alias set tgt "${OBJ_TARGET_URL:-$OBJ_SOURCE_URL}" "${OBJ_TARGET_USER:-$OBJ_SOURCE_USER}" "${OBJ_TARGET_PASSWORD:-$OBJ_SOURCE_PASSWORD}" >/dev/null

report() { # report <failed 0|1>
  [[ -n "${PUSHGATEWAY_HOST_PORT:-}" && "$mode" == mirror ]] || return 0
  local now body
  now="$(date +%s)"
  body="# TYPE payrudder_objects_last_run_timestamp_seconds gauge"$'\n'"payrudder_objects_last_run_timestamp_seconds $now"$'\n'
  [[ "$1" == 0 ]] && body+="# TYPE payrudder_objects_last_success_timestamp_seconds gauge"$'\n'"payrudder_objects_last_success_timestamp_seconds $now"$'\n'
  body+="# TYPE payrudder_objects_backup_failed gauge"$'\n'"payrudder_objects_backup_failed $1"$'\n'
  {
    exec 3<>"/dev/tcp/${PUSHGATEWAY_HOST_PORT%:*}/${PUSHGATEWAY_HOST_PORT##*:}" &&
      printf 'POST /metrics/job/payrudder_object_backup HTTP/1.0\r\nHost: %s\r\nContent-Length: %d\r\nContent-Type: text/plain\r\n\r\n%s' "$PUSHGATEWAY_HOST_PORT" "${#body}" "$body" >&3 &&
      read -r status <&3 && [[ "$status" == *' 20'* ]]
  } 2>/dev/null || echo "object-backup: could not report to the pushgateway" >&2
  exec 3>&- 2>/dev/null || true
}

failed=0
# Both sides must agree: "<" an object only in the source (it did not arrive), "!" a different one. ">" (only in the target) is a deleted object kept on purpose.
compare() { # compare <bucket>
  local line out
  out="$(mc diff "src/$1" "tgt/$tb/$prefix/$1" 2>&1)" || { echo "object-backup: could not compare $1: $out" >&2; failed=1; return; }
  while IFS= read -r line; do
    if [[ "$line" == '< '* || "$line" == '! '* ]]; then echo "object-backup: $1: $line" >&2; failed=1; fi
  done <<<"$out"
}
if [[ "$mode" == verify ]]; then
  for b in $buckets; do compare "$b"; done
elif [[ "$mode" == mirror ]]; then
  mc mb --ignore-existing "tgt/$tb" >/dev/null || failed=1
  for b in $buckets; do
    flags=(--overwrite)
    [[ "${OBJ_MIRROR_REMOVE:-}" == 1 ]] && flags+=(--remove)
    if ! mc mirror "${flags[@]}" "src/$b" "tgt/$tb/$prefix/$b"; then echo "object-backup: mirroring $b failed" >&2; failed=1; continue; fi
    compare "$b"
    echo "object-backup: $b -> $tb/$prefix/$b done"
  done
else
  for b in $buckets; do
    mc mb --ignore-existing "src/$b" >/dev/null || failed=1
    mc mirror --overwrite "tgt/$tb/$prefix/$b" "src/$b" || { echo "object-backup: restoring $b failed" >&2; failed=1; }
  done
fi
report "$failed"
exit "$failed"
