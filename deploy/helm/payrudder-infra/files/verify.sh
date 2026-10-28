#!/bin/sh
# The restore drill: restores the newest dump of each database into a scratch database, checks that it has tables, and drops the scratch database. Run it on a
# schedule (the compose service does) so a backup that cannot be restored is found before the day it is needed. Exit 1 when any database fails.
# Environment: PGHOST PGPORT PGUSER PGPASSWORD, BACKUP_DIR (default /backups), PUSHGATEWAY_URL (optional).
set -eu
dir="${BACKUP_DIR:-/backups}"
failed=0
checked=0
for latest in $(ls -1t "$dir"/*.dump 2>/dev/null | awk -F/ '{n=$NF; sub(/-[0-9]{8}T[0-9]{6}Z\.dump$/,"",n); if (!(n in seen)) {seen[n]=1; print $0}}'); do
  name="$(basename "$latest")"
  db="${name%%-[0-9]*}"
  scratch="verify_$db"
  checked=$((checked + 1))
  if "$(dirname "$0")/restore.sh" "$latest" "$scratch" --force >/dev/null 2>&1; then
    tables="$(psql -XAt -d "$scratch" -c "SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema')")"
    if [ "${tables:-0}" -gt 0 ]; then
      echo "verify: $db ok ($tables tables) from $name"
    else
      echo "verify: $db restored but empty ($name)" >&2
      failed=1
    fi
  else
    echo "verify: $db could not be restored from $name" >&2
    failed=1
  fi
  psql -X -d postgres -c "DROP DATABASE IF EXISTS \"$scratch\"" >/dev/null 2>&1 || true
done
[ "$checked" -gt 0 ] || { echo "verify: no dump found in $dir" >&2; failed=1; }

if [ -n "${PUSHGATEWAY_URL:-}" ]; then
  now="$(date +%s)"
  {
    if [ "$failed" = 0 ]; then
      echo "# TYPE payrudder_restore_verified_timestamp_seconds gauge"
      echo "payrudder_restore_verified_timestamp_seconds $now"
    fi
    echo "# TYPE payrudder_restore_verify_failed gauge"
    echo "payrudder_restore_verify_failed $failed"
  } | curl -fsS --max-time 10 --data-binary @- "${PUSHGATEWAY_URL%/}/metrics/job/payrudder_restore_verify" || true
fi
exit "$failed"
