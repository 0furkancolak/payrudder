#!/bin/sh
# One-shot, idempotent: waits for the broker, then creates the topics of docs/04-messaging.md (the same script as `make topics`).
set -eu

export RPK_BROKERS="${RPK_BROKERS:-${QUOTIFY_KAFKA_BROKERS:?}}"
until rpk cluster info >/dev/null 2>&1; do echo "waiting for $RPK_BROKERS"; sleep 3; done
exec sh /topics.sh
