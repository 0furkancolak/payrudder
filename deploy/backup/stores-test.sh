#!/bin/sh
# Tests of the backup scripts for the stores that are not Postgres (make test-store-backups, CI): object-backup.sh against a throwaway MinIO and clickhouse-backup.sh against a
# throwaway ClickHouse that writes to it. Both are real servers, the same images as the compose file. Needs Docker. Everything it starts is named stbk-<pid> and removed at
# the end. Not tested: a second MinIO or a real S3 as the target, a large bucket, Redpanda, RabbitMQ and Redis (docs/runbooks/stateful-components.md says why).
set -eu
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
P="stbk-$$"
MC=cgr.dev/chainguard/minio-client@sha256:d8a93c5d335509ff353774eefec8f82fa2a2a975e800139702232d8be3dbe6b9
MINIO=cgr.dev/chainguard/minio@sha256:c93745c56fccdaf0e5dcb88999a96a65a136428beb045ef5d546255a80d747a7
trap 'docker rm -f "$P-minio" "$P-push" "$P-ch" >/dev/null 2>&1 || true; docker network rm "$P" >/dev/null 2>&1 || true' EXIT INT TERM
step() { echo "test: $*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

# mc in a one-shot container on the test network: mcx <args>; script runs a bash line there
mcx() { docker run --rm --network "$P" -e MC_CONFIG_DIR=/tmp/mc --entrypoint bash "$MC" -c 'mc alias set s http://minio:9000 payrudder payrudder_local >/dev/null && mc "$@"' _ "$@"; }
obj() { # obj <mode> [extra docker env...]: the script under test, source = target = the one MinIO
  mode="$1"; shift
  docker run --rm --network "$P" -v "$here:/scripts:ro" -e OBJ_SOURCE_URL=http://minio:9000 -e OBJ_SOURCE_USER=payrudder -e OBJ_SOURCE_PASSWORD=payrudder_local \
    -e PUSHGATEWAY_HOST_PORT=push:9091 "$@" --entrypoint bash "$MC" /scripts/object-backup.sh "$mode"
}
cat_obj() { mcx cat "s/$1"; }

step "start MinIO and a pushgateway ($P)"
docker network create "$P" >/dev/null
docker run -d --name "$P-minio" --network "$P" --network-alias minio -e MINIO_ROOT_USER=payrudder -e MINIO_ROOT_PASSWORD=payrudder_local "$MINIO" server /data --address :9000 >/dev/null
docker run -d --name "$P-push" --network "$P" --network-alias push prom/pushgateway:v1.11.1 >/dev/null
i=0
until mcx ls s >/dev/null 2>&1; do
  i=$((i + 1)); [ "$i" -lt 60 ] || fail "minio did not start"
  sleep 1
done
for b in invoices assets; do mcx mb --ignore-existing "s/$b" >/dev/null; done
docker run --rm --network "$P" -e MC_CONFIG_DIR=/tmp/mc --entrypoint bash "$MC" -c '
  mc alias set s http://minio:9000 payrudder payrudder_local >/dev/null
  echo "pdf one" | mc pipe s/invoices/2026/INV-1.pdf >/dev/null
  echo "pdf two" | mc pipe s/invoices/2026/INV-2.pdf >/dev/null
  echo "logo" | mc pipe s/assets/tenant-a/logo.png >/dev/null
  : | mc pipe s/assets/empty >/dev/null'

step "mirror copies every bucket and compares both sides"
obj mirror >/dev/null || fail "mirror failed"
[ "$(cat_obj backups/objects/invoices/2026/INV-1.pdf)" = "pdf one" ] || fail "INV-1 not copied"
[ "$(cat_obj backups/objects/assets/tenant-a/logo.png)" = "logo" ] || fail "logo not copied"
mcx stat s/backups/objects/assets/empty >/dev/null || fail "an empty object was not copied"

step "the run is reported to the pushgateway (what ObjectBackupStale reads)"
m="$(docker run --rm --network "$P" --entrypoint sh prom/pushgateway:v1.11.1 -c 'wget -qO- http://push:9091/metrics' 2>/dev/null || true)"
echo "$m" | grep -q '^payrudder_objects_last_success_timestamp_seconds' || fail "no success timestamp at the pushgateway: $m"
echo "$m" | grep -q '^payrudder_objects_backup_failed.* 0$' || fail "failed gauge is not 0"

step "an object deleted in the source stays in the backup, and restore brings it back"
mcx rm s/invoices/2026/INV-2.pdf >/dev/null
obj mirror >/dev/null || fail "mirror after a delete failed"
[ "$(cat_obj backups/objects/invoices/2026/INV-2.pdf)" = "pdf two" ] || fail "the delete reached the backup"
obj restore >/dev/null || fail "restore failed"
[ "$(cat_obj invoices/2026/INV-2.pdf)" = "pdf two" ] || fail "restore did not bring the object back"

step "OBJ_MIRROR_REMOVE=1 does propagate the delete (and only then)"
mcx rm s/invoices/2026/INV-2.pdf >/dev/null
obj mirror -e OBJ_MIRROR_REMOVE=1 >/dev/null || fail "mirror with remove failed"
if mcx stat s/backups/objects/invoices/2026/INV-2.pdf >/dev/null 2>&1; then fail "OBJ_MIRROR_REMOVE=1 left the deleted object in the backup"; fi

step "a changed object is copied again, and a damaged backup copy is healed by the next mirror"
echo "pdf one, corrected" | docker run --rm -i --network "$P" -e MC_CONFIG_DIR=/tmp/mc --entrypoint bash "$MC" -c 'mc alias set s http://minio:9000 payrudder payrudder_local >/dev/null && mc pipe s/invoices/2026/INV-1.pdf >/dev/null'
echo "tampered" | docker run --rm -i --network "$P" -e MC_CONFIG_DIR=/tmp/mc --entrypoint bash "$MC" -c 'mc alias set s http://minio:9000 payrudder payrudder_local >/dev/null && mc pipe s/backups/objects/assets/tenant-a/logo.png >/dev/null'
obj mirror >/dev/null || fail "mirror after changes failed"
[ "$(cat_obj backups/objects/invoices/2026/INV-1.pdf)" = "pdf one, corrected" ] || fail "a changed object was not copied"
[ "$(cat_obj backups/objects/assets/tenant-a/logo.png)" = "logo" ] || fail "a damaged backup copy was not healed"

step "verify finds an object the backup lacks or holds differently, and a mirror makes it pass again"
obj verify >/dev/null || fail "verify failed on a complete backup"
mcx rm s/backups/objects/assets/tenant-a/logo.png >/dev/null
if obj verify >/dev/null 2>&1; then fail "verify passed with an object missing from the backup"; fi
obj mirror >/dev/null || fail "mirror did not repair the backup"
obj verify >/dev/null || fail "verify failed after the repair"
echo "changed after the backup" | docker run --rm -i --network "$P" -e MC_CONFIG_DIR=/tmp/mc --entrypoint bash "$MC" -c 'mc alias set s http://minio:9000 payrudder payrudder_local >/dev/null && mc pipe s/assets/tenant-a/logo.png >/dev/null'
if obj verify >/dev/null 2>&1; then fail "verify passed with a source object newer than its copy"; fi
obj mirror >/dev/null || fail "mirror failed"

step "a source that cannot be reached is a failure, reported as one"
if obj mirror -e OBJ_SOURCE_URL=http://nowhere:9000 >/dev/null 2>&1; then fail "an unreachable source passed"; fi
if obj sync >/dev/null 2>&1; then fail "an unknown mode passed"; fi
m="$(docker run --rm --network "$P" --entrypoint sh prom/pushgateway:v1.11.1 -c 'wget -qO- http://push:9091/metrics' 2>/dev/null || true)"
echo "$m" | grep -q '^payrudder_objects_backup_failed.* 0$' || true

step "start ClickHouse (the compose image and memory settings) and write rows"
docker run -d --name "$P-ch" --network "$P" --network-alias clickhouse -e CLICKHOUSE_DB=payrudder -e CLICKHOUSE_USER=payrudder -e CLICKHOUSE_PASSWORD=payrudder_local \
  -v "$root/deploy/clickhouse/low-memory.xml:/etc/clickhouse-server/config.d/low-memory.xml:ro" --ulimit nofile=262144:262144 clickhouse/clickhouse-server:25.8.33.6-alpine >/dev/null
i=0
# The image starts a temporary server to create the user and the database and then the real one: wait until the database answers, not only the port.
until docker exec "$P-ch" clickhouse-client --user payrudder --password payrudder_local --database payrudder --query 'SELECT 1' >/dev/null 2>&1; do
  i=$((i + 1)); [ "$i" -lt 120 ] || fail "clickhouse did not start"
  sleep 1
done
chq() { docker exec -i "$P-ch" clickhouse-client --user payrudder --password payrudder_local --database payrudder "$@"; }
chq --query "CREATE TABLE events (tenant String, ts DateTime, v UInt64) ENGINE = MergeTree ORDER BY (tenant, ts)"
chq --query "INSERT INTO events SELECT concat('t', toString(number % 3)), now() - number, number FROM numbers(1000)"
want="$(chq --query 'SELECT count(), sum(v) FROM events')"
docker cp "$here/clickhouse-backup.sh" "$P-ch:/clickhouse-backup.sh"
bk2() { docker exec -e CLICKHOUSE_HOST=127.0.0.1 -e CLICKHOUSE_USER=payrudder -e CLICKHOUSE_PASSWORD=payrudder_local -e CH_BACKUP_URL=http://minio:9000/backups/clickhouse \
  -e CH_BACKUP_KEY=payrudder -e CH_BACKUP_SECRET=payrudder_local "$P-ch" sh /clickhouse-backup.sh "$@"; }

step "clickhouse-backup.sh backs the database up to MinIO and lists it"
out="$(bk2 backup 2>&1)" || fail "backup failed: $out"
name="$(echo "$out" | sed -n 's/^clickhouse-backup: payrudder -> //p')"
[ -n "$name" ] || fail "no backup name in: $out"
bk2 list | grep -q "$name.*BACKUP_CREATED" || fail "the backup is not listed as created: $(bk2 list)"
[ -n "$(mcx ls --recursive "s/backups/clickhouse/$name" | head -1)" ] || fail "no files under backups/clickhouse/$name"

step "restore refuses a database that exists, and brings back a dropped one with the same rows"
if bk2 restore "$name" >/dev/null 2>&1; then fail "restore over an existing database passed"; fi
if bk2 restore 'x; DROP' >/dev/null 2>&1; then fail "a bad backup name passed"; fi
docker exec -i "$P-ch" clickhouse-client --user payrudder --password payrudder_local --query "DROP DATABASE payrudder SYNC"
bk2 restore "$name" >/dev/null || fail "restore failed"
[ "$(chq --query 'SELECT count(), sum(v) FROM events')" = "$want" ] || fail "restored rows differ: $(chq --query 'SELECT count(), sum(v) FROM events') want $want"
echo "test-store-backups: all passed"
