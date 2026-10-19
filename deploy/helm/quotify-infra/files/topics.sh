#!/bin/sh
# Creates the Kafka topics of docs/04-messaging.md#kafka-topics with local retention (RF 1, or $RF). Idempotent.
# Runs inside the redpanda container:  docker compose exec -T redpanda sh -s < deploy/compose/redpanda/topics.sh
set -eu

DAY=86400000

# create <name> <partitions> <retention-days | compact>
create() {
  name=$1 partitions=$2 policy=$3
  if [ "$policy" = compact ]; then
    set -- -c cleanup.policy=compact -c min.compaction.lag.ms=3600000 -c delete.retention.ms=$DAY
  else
    set -- -c cleanup.policy=delete -c "retention.ms=$((policy * DAY))"
  fi
  out=$(rpk topic create "$name" -p "$partitions" -r "${RF:-1}" "$@" 2>&1) || true
  case "$out" in
    *TOPIC_ALREADY_EXISTS*) echo "exists   $name" ;;
    *OK*) echo "created  $name" ;;
    *) echo "FAILED   $name: $out" >&2; exit 1 ;;
  esac
}

create usage.events.v1              12 30
create usage.events.dlq.v1           3 30
create usage.limit-exceeded.v1       6 90
create usage.limit-exceeded.dlq.v1   1 30
create plans.entitlements.v1         6 compact
create billing.invoices.v1           6 400
create billing.invoices.dlq.v1       1 30
