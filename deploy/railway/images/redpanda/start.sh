#!/bin/sh
# Single-node Redpanda for Railway's private network. The broker advertises its own private DNS name (RAILWAY_PRIVATE_DOMAIN), so clients in other
# services reach it as redpanda.railway.internal:9092. Topics are not auto-created: the redpanda-init job creates them (docs/04-messaging.md).
# REDPANDA_BIND is the address to listen on: 0.0.0.0 (IPv4, the default) or "::" for a private network that is IPv6 only.
set -eu

: "${RAILWAY_PRIVATE_DOMAIN:?not set: this image runs on Railway}"
BIND="${REDPANDA_BIND:-0.0.0.0}"
case "$BIND" in *:*) BIND="[$BIND]" ;; esac

exec rpk redpanda start \
  --mode dev-container \
  --smp 1 \
  --memory "${REDPANDA_MEMORY:-512M}" \
  --reserve-memory 0M \
  --overprovisioned \
  --node-id 0 \
  --check=false \
  --kafka-addr "internal://${BIND}:9092" \
  --advertise-kafka-addr "internal://${RAILWAY_PRIVATE_DOMAIN}:9092" \
  --rpc-addr "${BIND}:33145" \
  --advertise-rpc-addr "${RAILWAY_PRIVATE_DOMAIN}:33145" \
  --set redpanda.auto_create_topics_enabled=false
