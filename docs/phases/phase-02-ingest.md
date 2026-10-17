# Phase 02 — ingest-svc

## Goal

`POST /v1/events` durably accepts usage events exactly once per idempotency key and publishes them to
`usage.events.v1` through a transactional outbox.

## Scope

**In:** `proto/quotify/events/v1/usage_event.proto`, `proto/quotify/ingest/v1`, ingest-svc handler, `ingest_db` schema
(partitioned `idempotency_keys`, `outbox`), generic `pkg/outbox` relay with a Kafka sink, `pkg/kafkax` producer factory,
gateway `POST /v1/events`, topic creation (`make topics`), `GetOutboxWatermark`.

**Out of scope:** consumers (Phase 03+), AMQP sink for the outbox (Phase 05).

## Tasks

- [ ] `UsageEvent` proto + golden fixture `proto/testdata/UsageEvent/v0.1.0.bin`
- [ ] `IngestService.IngestEvents` + `GetOutboxWatermark` protos
- [ ] Migrations: `idempotency_keys` (monthly partitions, bucketed `created_at`), `outbox`, `enqueue_outbox` SECURITY DEFINER function
- [ ] Partition maintenance job: create next 2 months, drop partitions older than 45 days
- [ ] Validation rules + error codes per [ingest-svc.md](../03-services/ingest-svc.md#validation-rules)
- [ ] Cached `GetMeter` / `ResolveCustomer` clients (LRU + TTL)
- [ ] Canonical payload hash (sorted properties) + key-reuse detection
- [ ] Bulk insert with `unnest` + `ON CONFLICT DO NOTHING RETURNING`. Previous-month partition check
- [ ] `ingested_at = clock_timestamp()` from Postgres. `SET LOCAL transaction_timeout = '5s'`
- [ ] `pkg/outbox`: relay with `SKIP LOCKED`, `LISTEN/NOTIFY` wakeup + 50 ms poll, batch 1000, delete-after-ack, metrics
- [ ] `pkg/kafkax`: producer factory (idempotent, acks=all, zstd), header helpers (`ce-type`, `tenant-id`, trace)
- [ ] Trace context captured into `outbox.headers` and restored as Kafka headers
- [ ] Outbox back-pressure: `RESOURCE_EXHAUSTED` above 5M rows (configurable)
- [ ] Fault hook `RELAY_CRASH_AFTER_PRODUCE` (build tag `chaos`)
- [ ] gateway route + per-event rate charging + batch size limits
- [ ] `make topics` with `rpk` using configs from [04-messaging.md](../04-messaging.md)

## Deliverables

- ingest-svc image (handler + relay in one binary).
- Events visible in Redpanda Console with decoded protobuf (descriptor uploaded).

## Acceptance criteria

- [ ] Integration: the same idempotency key sent 3× concurrently → exactly 1 outbox row and 1 Kafka record. Responses: 1 `ACCEPTED`, 2 `DUPLICATE`, all with the same `event_id`
- [ ] Integration: same key, different quantity → `REJECTED / IDEMPOTENCY_KEY_REUSED`
- [ ] Integration: key accepted on the last day of a month, retried on the 1st of the next → `DUPLICATE` (cross-partition check)
- [ ] Integration: Kafka container paused for 60 s while ingesting 10k events → every `ACCEPTED` event appears in Kafka after unpause, count exact
- [ ] Integration: `RELAY_CRASH_AFTER_PRODUCE=0.2` → every event in Kafka ≥ 1 time, and duplicates share `event_id`
- [ ] Integration: `GetOutboxWatermark` returns the oldest unpublished `ingested_at`, and unset when drained
- [ ] Unit: validation table (too old, future, negative quantity, unknown meter/customer)
- [ ] Kafka record headers contain `traceparent`, `ce-type=quotify.events.v1.UsageEvent`, `tenant-id`

## Dependencies

Phase 01 (tenants, meters, customers, gateway auth).

## Risks / open questions

- Partitioned idempotency table complexity vs. an unpartitioned table with batched `DELETE`. Benchmark both briefly and record the decision in an ADR addendum.
- Relay ordering across concurrent relays is "mostly ordered". Confirm no consumer relies on strict order (by design none should).
- Should `idempotency_key` be mandatory for live keys? (Planned: yes.)
