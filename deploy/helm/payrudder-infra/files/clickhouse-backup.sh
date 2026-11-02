#!/bin/sh
# Backs up and restores the analytics database with ClickHouse's own BACKUP and RESTORE to S3-compatible storage: clickhouse-backup.sh backup | restore <name> | list.
# No tool beyond clickhouse-client (it is in the server image). The analytics data is derived: the event stream and the aggregator's replay can rebuild it (see
# docs/runbooks/stateful-components.md), so this is the quick way back, not the only one. A backup is a directory of files named <name> under CH_BACKUP_URL; retention is the
# bucket's lifecycle rule. restore needs the database to be empty or missing on the server (a half-restored table is worse than none): DROP DATABASE first.
#
# Environment: CLICKHOUSE_HOST (default clickhouse) CLICKHOUSE_PORT (default 9000) CLICKHOUSE_USER CLICKHOUSE_PASSWORD CLICKHOUSE_DB (default payrudder);
# CH_BACKUP_URL (e.g. http://minio:9000/backups/clickhouse), CH_BACKUP_KEY, CH_BACKUP_SECRET; PUSHGATEWAY_URL (optional; needs curl).
set -eu
mode="${1:?usage: clickhouse-backup.sh backup | restore <name> | list}"
db="${CLICKHOUSE_DB:-payrudder}"
url="${CH_BACKUP_URL:?CH_BACKUP_URL}"
ch() { clickhouse-client --host "${CLICKHOUSE_HOST:-clickhouse}" --port "${CLICKHOUSE_PORT:-9000}" --user "${CLICKHOUSE_USER:-default}" --password "${CLICKHOUSE_PASSWORD:-}" "$@"; }
# the key and secret go on the command line of clickhouse-client; they are visible to this container's processes only
target() { printf "S3('%s/%s', '%s', '%s')" "${url%/}" "$1" "${CH_BACKUP_KEY:?CH_BACKUP_KEY}" "${CH_BACKUP_SECRET:?CH_BACKUP_SECRET}"; }
case "$mode" in
  backup)
    name="${db}-$(date -u +%Y%m%dT%H%M%SZ)"
    failed=0
    ch --query "BACKUP DATABASE \`$db\` TO $(target "$name")" || failed=1
    if [ "$failed" = 0 ]; then echo "clickhouse-backup: $db -> $name"; else echo "clickhouse-backup: backup of $db failed" >&2; fi
    if [ -n "${PUSHGATEWAY_URL:-}" ]; then
      now="$(date +%s)"
      { echo "payrudder_clickhouse_backup_last_run_timestamp_seconds $now"; [ "$failed" = 0 ] && echo "payrudder_clickhouse_backup_last_success_timestamp_seconds $now"; echo "payrudder_clickhouse_backup_failed $failed"; } |
        curl -fsS --max-time 10 --data-binary @- "${PUSHGATEWAY_URL%/}/metrics/job/payrudder_clickhouse_backup" || echo "clickhouse-backup: could not report to the pushgateway" >&2
    fi
    exit "$failed"
    ;;
  restore)
    name="${2:?usage: clickhouse-backup.sh restore <name>}"
    case "$name" in *[!A-Za-z0-9_.-]*) echo "clickhouse-backup: bad backup name" >&2; exit 2 ;; esac
    if [ "$(ch --query "SELECT count() FROM system.databases WHERE name = '$db'")" != 0 ]; then
      echo "clickhouse-backup: database $db exists; drop it first (DROP DATABASE \`$db\`) so the restore is of the whole backup" >&2
      exit 1
    fi
    ch --query "RESTORE DATABASE \`$db\` FROM $(target "$name")"
    echo "clickhouse-backup: $name -> $db"
    ;;
  list)
    ch --query "SELECT name, status, start_time, end_time FROM system.backups ORDER BY start_time FORMAT TSVWithNames"
    ;;
  *) echo "usage: clickhouse-backup.sh backup | restore <name> | list" >&2; exit 2 ;;
esac
