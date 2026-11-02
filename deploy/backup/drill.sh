#!/bin/sh
# The point-in-time recovery drill (make backup-drill; CI runs it too): proves, on a throwaway stack, that the database can be restored to a chosen minute from what the
# stack really archives, and that the restored data is consistent. A backup that was never restored is worthless; this is the acceptance of plan 16, not the cron job.
#
# It brings up the compose stack (deploy/compose: postgres, minio, pushgateway) with compose.pitr.yaml under its own project name bkdrill-<pid> (own containers, network
# and volumes; the stack you work in is not touched, no host port is published), then:
#   1. builds the billing and payments schemas from their migrations and seeds 20 invoices, lines, credit lots and payments
#   2. starts the sidecar (pitr-agent): marker, base backup, WAL shipping; checks that a second base backup within its minimum age is skipped; takes the nightly
#      dumps to storage and runs the dump drill (verify.sh with invariants)
#   3. adds 10 more invoices and a marker row "drill-before-T", records the row counts and picks the target time T
#   4. the "incident" after T: all invoices deleted, more payments, a marker "drill-after-T"; the WAL is switched, archived and shipped
#   5. refuses a target from before the first base backup, restores T into a new volume (pitr-restore.sh), starts a scratch Postgres on it, waits for recovery to finish
#   6. checks: invariants hold, the row counts are those of T (not those of before the incident, not those after), the marker before T is there and the one after is not,
#      the metrics the alerts read reached the pushgateway
#   7. prints the measured RTO (restore start to checks passed, including the install of the aws CLI in the throwaway container) against the 1 h objective
# Needs Docker with Compose v2.24+ (!reset), about 1 GB of RAM, network for the first image pulls and `apk add aws-cli`. KEEP=1 leaves the stack up for inspection
# (remove it with: docker compose -p <project> down -v). Not verified by it: real S3, a real cluster, a crash instead of a clean stop.
set -eu
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
P="bkdrill-$$"
RTO_TARGET=3600
dc() { docker compose -p "$P" -f "$root/deploy/compose/compose.yaml" -f "$root/deploy/compose/compose.pitr.yaml" -f "$root/deploy/compose/compose.drill.yaml" --profile ops --profile obs --profile init "$@"; }
pg() { dc exec -T -e PGUSER=postgres postgres "$@"; }
sql() { pg psql -X -qAt -v ON_ERROR_STOP=1 -d "$1" -c "$2"; }
agent() { dc exec -T pitr-agent "$@"; }
step() { echo "drill: $*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

cleanup() {
  rc=$?
  if [ "$rc" != 0 ]; then
    echo "--- the drill failed; last log lines ---" >&2
    dc logs --tail 25 postgres pitr-agent 2>&1 | tail -60 >&2 || true
    docker logs --tail 25 "$P-scratch" 2>&1 | tail -40 >&2 || true
  fi
  if [ "${KEEP:-}" = 1 ]; then echo "drill: KEEP=1, project $P stays up"; return; fi
  docker rm -f "$P-scratch" >/dev/null 2>&1 || true
  # Throwaway data: no graceful shutdown (-t 1).
  dc down -v --remove-orphans -t 1 >/dev/null 2>&1 || { sleep 10; dc down -v --remove-orphans -t 1 >/dev/null 2>&1; } || echo "drill: could not remove project $P; remove it with: docker compose -p $P down -v" >&2
  docker volume rm "${P}_restore" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

# waits (up to $1 seconds) until the command succeeds
wait_for() {
  limit="$1"; what="$2"; shift 2
  i=0
  until "$@" >/dev/null 2>&1; do
    i=$((i + 1)); [ "$i" -lt "$limit" ] || fail "timed out waiting for $what"
    sleep 1
  done
}

step "start postgres (archiving), minio and the pushgateway as project $P"
dc up -d --wait postgres minio pushgateway >/dev/null
dc run --rm minio-init >/dev/null
wait_for 120 "postgres to finish initialising" pg pg_isready -q -h 127.0.0.1
[ "$(sql postgres 'SHOW archive_mode')" = on ] || fail "archive_mode is not on"

step "build the schemas from the migrations and seed invoices 1-20"
pg sh /scripts/testdata/schema.sh billing_db /m/billing
pg sh /scripts/testdata/schema.sh payments_db /m/payments
for d in billing payments; do pg psql -X -q -v ON_ERROR_STOP=1 -v lo=1 -v hi=20 -d "${d}_db" -f "/scripts/testdata/seed-$d.sql"; done

step "start the sidecar: marker, base backup and WAL shipping (installs the aws CLI first)"
dc up -d pitr-agent >/dev/null
wait_for 240 "the first base backup" sh -c "docker exec $P-pitr-agent-1 aws s3 ls s3://backups/pitr/base/ --recursive | grep -q start_wal"

step "another run of the base backup script within its minimum age takes nothing new"
bases() { agent aws s3 ls s3://backups/pitr/base/ | grep -c PRE; }
[ "$(bases)" = 1 ] || fail "expected one base backup, found $(bases)"
agent env BASEBACKUP_MIN_AGE=86400 sh /scripts/pitr-basebackup.sh | grep -q 'nothing to do' || fail "the base backup script ignored its minimum age"
[ "$(bases)" = 1 ] || fail "a second base backup was taken within the minimum age"

step "nightly dumps to storage, then the dump drill (verify.sh: restore each dump, check the invariants)"
dc run --rm --no-deps -e BACKUP_DATABASES="billing_db payments_db" -e PUSHGATEWAY_URL=http://pushgateway:9091 --entrypoint sh backup -c \
  'apk add -q --no-cache curl aws-cli && sh /scripts/backup.sh && sh /scripts/verify.sh && aws s3 ls s3://backups/dumps/ --recursive'

step "add invoices 21-30 and the marker drill-before-T, record the counts, pick T"
for d in billing payments; do pg psql -X -q -v ON_ERROR_STOP=1 -v lo=21 -v hi=30 -d "${d}_db" -f "/scripts/testdata/seed-$d.sql"; done
sql postgres "CREATE TABLE IF NOT EXISTS payrudder_pitr_markers (id bigserial PRIMARY KEY, written_at timestamptz NOT NULL DEFAULT now(), note text); INSERT INTO payrudder_pitr_markers (note) VALUES ('drill-before-T')" >/dev/null
expected="$(pg sh /scripts/invariants.sh billing_db payments_db | grep '^counts ')"
T="$(sql postgres "SELECT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US')")"
echo "$expected" | sed 's/^/  expected at T: /'
echo "  T = $T UTC"
sleep 2

step "the incident after T: every invoice deleted, more payments, marker drill-after-T"
sql billing_db "SET session_replication_role = replica; DELETE FROM invoice_lines; DELETE FROM invoices" >/dev/null
pg psql -X -q -v ON_ERROR_STOP=1 -v lo=31 -v hi=35 -d payments_db -f /scripts/testdata/seed-payments.sql
sql postgres "INSERT INTO payrudder_pitr_markers (note) VALUES ('drill-after-T')" >/dev/null
archived="$(sql postgres 'SELECT archived_count FROM pg_stat_archiver')"
sql postgres "SELECT pg_switch_wal()" >/dev/null
wait_for 60 "Postgres to archive the switched segment" sh -c "[ \"\$(docker exec -e PGUSER=postgres $P-postgres-1 psql -XAt -d postgres -c 'SELECT archived_count FROM pg_stat_archiver')\" -gt $archived ]"
wait_for 60 "the sidecar to ship the spool" sh -c "[ -z \"\$(docker exec $P-postgres-1 sh -c 'ls /var/run/postgresql/wal-spool | grep gz\$ || true')\" ]"

step "restore T into a new volume, start a scratch Postgres on it and let recovery finish"
docker volume create "${P}_restore" >/dev/null
start="$(date +%s)"
dc run --rm --no-deps -v "${P}_restore:/var/lib/postgresql/data" --entrypoint sh pitr-agent -c \
  "apk add -q --no-cache aws-cli
   if sh /scripts/pitr-restore.sh '2000-01-01 00:00:00' /var/lib/postgresql/data 2>/tmp/refused; then echo 'a target before every base backup was accepted' >&2; exit 1; fi
   grep -q 'no complete base backup' /tmp/refused || { cat /tmp/refused >&2; exit 1; }
   sh /scripts/pitr-restore.sh '$T' /var/lib/postgresql/data"
docker run -d --name "$P-scratch" --network "${P}_default" -v "${P}_restore:/var/lib/postgresql/data" -v "$here:/scripts:ro" postgres:17.11-alpine >/dev/null
scratch() { docker exec -e PGUSER=postgres "$P-scratch" "$@"; }
i=0
until [ "$(scratch psql -XAt -d postgres -c 'SELECT pg_is_in_recovery()' 2>/dev/null || true)" = f ]; do
  i=$((i + 1)); [ "$i" -lt 180 ] || fail "the scratch server did not finish recovery"
  [ "$(docker inspect -f '{{.State.Running}}' "$P-scratch")" = true ] || fail "the scratch server stopped during recovery"
  sleep 1
done

step "check the restored copy"
scratch sh /scripts/invariants.sh billing_db payments_db | tee /dev/stderr | grep '^counts ' >"${TMPDIR:-/tmp}/$P.counts" || fail "invariants failed on the restored copy"
end="$(date +%s)"
[ "$(cat "${TMPDIR:-/tmp}/$P.counts")" = "$expected" ] || { echo "restored: $(cat "${TMPDIR:-/tmp}/$P.counts")" >&2; fail "row counts differ from those at T"; }
rm -f "${TMPDIR:-/tmp}/$P.counts"
notes="$(scratch psql -XAt -d postgres -c "SELECT note FROM payrudder_pitr_markers WHERE note LIKE 'drill-%'")"
[ "$notes" = drill-before-T ] || fail "markers in the restored copy: '$notes' (want only drill-before-T)"
echo "  invariants hold, row counts are those of T, marker drill-before-T is there, drill-after-T is not"

step "the metrics the alert rules read reached the pushgateway"
metrics="$(agent curl -fsS http://pushgateway:9091/metrics)"
for m in payrudder_wal_last_shipped_timestamp_seconds payrudder_wal_spool_files payrudder_pitr_basebackup_last_success_timestamp_seconds payrudder_backup_last_success_timestamp_seconds payrudder_restore_verified_timestamp_seconds; do
  echo "$metrics" | grep -q "^$m" || fail "metric $m is not at the pushgateway"
done
echo "  all five present"

rto=$((end - start))
echo "drill: RESTORED to $T UTC in ${rto}s (RTO objective ${RTO_TARGET}s, RPO objective 900s). The drill passed."
[ "$rto" -le "$RTO_TARGET" ] || fail "the restore took longer than the RTO objective"
