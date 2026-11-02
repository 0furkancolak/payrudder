#!/bin/sh
# Tests of the backup scripts against a throwaway Postgres container (make test-backup-scripts, CI): backup.sh (dumps, retention, an unreadable dump is not a backup),
# restore.sh (create, refuse to replace without --force, replace with it), verify.sh and invariants.sh (a good copy passes, torn data fails and is reported to the
# pushgateway), wal-archive.sh (idempotent, refuses other content under a name) and the argument checks of pitr-restore.sh. The S3 side and point-in-time recovery itself
# need MinIO and are exercised by drill.sh (make backup-drill). Needs Docker. The container is named bktest-<pid> and removed at the end.
set -eu
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
name="bktest-$$"
trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT INT TERM

step() { echo "test: $*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
# in the container, as the superuser; stdout and stderr both come back
run() { docker exec -e PGUSER=postgres -e BACKUP_DIR=/backups -e BACKUP_DATABASES="billing_db payments_db" "$name" "$@" 2>&1; }
# the command must succeed / must fail
must() { run "$@" >/dev/null || fail "expected success: $*"; }
mustnot() { if run "$@" >/dev/null; then fail "expected failure: $*"; fi; }
sql() { docker exec -e PGUSER=postgres "$name" psql -X -qAt -v ON_ERROR_STOP=1 -d "$1" -c "$2"; }
dumps() { docker exec "$name" sh -c "ls -1 /backups/$1-*.dump 2>/dev/null | wc -l" | tr -d ' '; }

step "start a throwaway postgres ($name)"
docker run -d --name "$name" -e POSTGRES_PASSWORD=pw \
  -v "$root/deploy/compose/postgres/01-databases.sh:/docker-entrypoint-initdb.d/01-databases.sh:ro" \
  -v "$here:/scripts:ro" -v "$root/services/billing-svc/migrations:/m/billing:ro" -v "$root/services/payments-worker/migrations:/m/payments:ro" \
  postgres:17.11-alpine >/dev/null
i=0
until docker exec "$name" pg_isready -q -h 127.0.0.1; do
  i=$((i + 1)); [ "$i" -lt 60 ] || fail "postgres did not start"
  sleep 1
done
must sh /scripts/testdata/schema.sh billing_db /m/billing
must sh /scripts/testdata/schema.sh payments_db /m/payments
for db in billing payments; do must psql -X -q -v ON_ERROR_STOP=1 -v lo=1 -v hi=10 -d "${db}_db" -f "/scripts/testdata/seed-$db.sql"; done
docker exec "$name" sh -c 'mkdir -p /stub && printf "#!/bin/sh\ncat >> /tmp/pushed\n" > /stub/curl && chmod +x /stub/curl'

step "invariants.sh passes on consistent data and counts the rows"
out="$(run sh /scripts/invariants.sh billing_db payments_db)" || fail "invariants failed on good data: $out"
echo "$out" | grep -q '^counts billing_db credit_ledger=20 credit_lots=10 invoice_lines=30 invoices=10 $' || fail "unexpected counts: $out"
echo "$out" | grep -q '^counts payments_db manual_receipts=2 payment_ledger=12 $' || fail "unexpected counts: $out"

step "backup.sh writes a readable dump per database and keeps the newest BACKUP_KEEP"
for n in 1 2 3; do must env BACKUP_KEEP=2 sh /scripts/backup.sh; sleep 1; done
[ "$(dumps billing_db)" = 2 ] && [ "$(dumps payments_db)" = 2 ] || fail "retention: billing_db $(dumps billing_db) dumps, payments_db $(dumps payments_db), want 2 each"

step "backup.sh does not call an unreadable dump a backup"
docker exec "$name" sh -c 'mkdir -p /broken && printf "#!/bin/sh\nexit 1\n" > /broken/pg_restore && chmod +x /broken/pg_restore'
before="$(dumps billing_db)"
mustnot env PATH=/broken:/usr/local/bin:/usr/bin:/bin BACKUP_KEEP=9 sh /scripts/backup.sh
[ "$(dumps billing_db)" = "$before" ] || fail "an unreadable dump was kept"

step "restore.sh creates, refuses to replace without --force, replaces with it, and checks the name"
newest="$(docker exec "$name" sh -c 'ls -1t /backups/billing_db-*.dump | head -1')"
must sh /scripts/restore.sh "$newest" restore_check
[ "$(sql restore_check 'SELECT count(*) FROM invoices')" = 10 ] || fail "restored copy has other data"
mustnot sh /scripts/restore.sh "$newest" restore_check
must sh /scripts/restore.sh "$newest" restore_check --force
mustnot sh /scripts/restore.sh "$newest" 'bad name;'

step "verify.sh passes on a good dump and reports it"
must sh -c 'rm -f /tmp/pushed'
out="$(run env PUSHGATEWAY_URL=http://pushgateway:9091 PATH=/stub:/usr/local/bin:/usr/bin:/bin sh /scripts/verify.sh)" || fail "verify failed on good dumps: $out"
echo "$out" | grep -q 'billing_db ok .*invariants hold' || fail "verify output: $out"
[ "$(docker exec "$name" grep -c '^payrudder_restore_verify_failed 0$' /tmp/pushed)" = 1 ] || fail "verify did not report success"
[ -z "$(docker exec "$name" psql -XAt -U postgres -d postgres -c "SELECT datname FROM pg_database WHERE datname LIKE 'verify\_%'")" ] || fail "verify left a scratch database"

step "invariants.sh finds torn invoice totals, credit lots and receipts"
sql billing_db "SET session_replication_role = replica; UPDATE invoices SET total_minor = total_minor + 1 WHERE number = 'INV-2026-000003'" >/dev/null
out="$(run sh /scripts/invariants.sh billing_db)" && fail "invoice total off by one passed: $out"
echo "$out" | grep -q 'invoice totals differ from the sum of their lines: 1 violating' || fail "invoice message: $out"
sql billing_db "SET session_replication_role = replica; UPDATE invoices SET total_minor = total_minor - 1 WHERE number = 'INV-2026-000003'; UPDATE credit_lots SET remaining = 71 WHERE id = md5('lot1')::uuid" >/dev/null
out="$(run sh /scripts/invariants.sh billing_db)" && fail "credit lot off by one passed: $out"
echo "$out" | grep -q 'credit lot balances differ from their ledger: 1 violating' || fail "credit lot message: $out"
sql billing_db "SET session_replication_role = replica; UPDATE credit_lots SET remaining = 70 WHERE id = md5('lot1')::uuid; DELETE FROM invoice_lines WHERE invoice_id = md5('inv4')::uuid AND kind = 'TAX'" >/dev/null
out="$(run sh /scripts/invariants.sh billing_db)" && fail "a lost line passed: $out"
sql payments_db "SET session_replication_role = replica; UPDATE manual_receipts SET amount_minor = amount_minor + 1 WHERE bank_reference = 'bt_5'" >/dev/null
out="$(run sh /scripts/invariants.sh payments_db)" && fail "a receipt off by one passed: $out"
echo "$out" | grep -q 'manual receipts differ from their ledger rows: 1 violating' || fail "receipt message: $out"
sql payments_db "SET session_replication_role = replica; UPDATE manual_receipts SET amount_minor = amount_minor - 1 WHERE bank_reference = 'bt_5'" >/dev/null

step "verify.sh fails, and says so to the pushgateway, when the newest dump holds torn data"
sql billing_db "SET session_replication_role = replica; UPDATE invoices SET total_minor = total_minor + 1 WHERE number = 'INV-2026-000003'" >/dev/null
sleep 1
must env BACKUP_KEEP=2 sh /scripts/backup.sh
must sh -c 'rm -f /tmp/pushed'
out="$(run env PUSHGATEWAY_URL=http://pushgateway:9091 PATH=/stub:/usr/local/bin:/usr/bin:/bin sh /scripts/verify.sh)" && fail "verify passed on a torn dump: $out"
echo "$out" | grep -q 'billing_db restored but its data is inconsistent' || fail "verify output: $out"
[ "$(docker exec "$name" grep -c '^payrudder_restore_verify_failed 1$' /tmp/pushed)" = 1 ] || fail "verify did not report the failure"

step "wal-archive.sh keeps a compressed copy, accepts the same file again and refuses other content under a name"
docker exec "$name" sh -c 'printf "segment one" > /tmp/seg1; printf "segment two" > /tmp/seg2'
must env WAL_SPOOL=/tmp/spool sh /scripts/wal-archive.sh /tmp/seg1 000000010000000000000007
[ "$(docker exec "$name" gzip -dc /tmp/spool/000000010000000000000007.gz)" = "segment one" ] || fail "the spooled WAL differs"
must env WAL_SPOOL=/tmp/spool sh /scripts/wal-archive.sh /tmp/seg1 000000010000000000000007
mustnot env WAL_SPOOL=/tmp/spool sh /scripts/wal-archive.sh /tmp/seg2 000000010000000000000007
[ -z "$(docker exec "$name" sh -c 'ls /tmp/spool | grep tmp || true')" ] || fail "a half-written file stayed in the spool"

step "pitr-restore.sh refuses a target that is not in UTC, a non-empty data directory, and no argument"
docker exec "$name" sh -c 'mkdir -p /tmp/pgd && touch /tmp/pgd/PG_VERSION'
mustnot env WAL_S3_URL=s3://x/y sh /scripts/pitr-restore.sh '2026-11-01 12:00:00+02' /tmp/empty
mustnot env WAL_S3_URL=s3://x/y sh /scripts/pitr-restore.sh yesterday /tmp/empty
mustnot env WAL_S3_URL=s3://x/y sh /scripts/pitr-restore.sh latest /tmp/pgd
mustnot sh /scripts/pitr-restore.sh latest /tmp/empty
echo "test-backup-scripts: all passed"
