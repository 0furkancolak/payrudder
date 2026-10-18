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

- [x] `UsageEvent` proto + golden fixture `proto/testdata/UsageEvent/v0.1.0.bin`
- [x] `IngestService.IngestEvents` + `GetOutboxWatermark` protos
- [x] Migrations: `idempotency_keys` (monthly partitions, bucketed `created_at`), `outbox`, `enqueue_outbox` SECURITY DEFINER function
- [x] Partition maintenance job: create next 2 months, drop partitions older than 45 days
- [x] Exact decimal quantity parsing → scaled `int64` (`pkg/quantity`, no floats, `forbidigo`), `INVALID_QUANTITY_PRECISION`
- [x] Stamp `period_id` with `pkg/period.PeriodOf(event_time, tz)` (tenant time zone from metadata)
- [x] Idempotency key required for live keys (`IDEMPOTENCY_KEY_REQUIRED`). Sandbox: generated `auto:` key
- [x] Validation rules + error codes per [ingest-svc.md](../03-services/ingest-svc.md#validation-rules)
- [x] Cached `GetMeter` / `ResolveCustomer` clients (LRU + TTL)
- [x] Canonical payload hash (sorted properties) + key-reuse detection
- [x] Bulk insert with `unnest` + `ON CONFLICT DO NOTHING RETURNING`. Previous-month partition check
- [x] `ingested_at = clock_timestamp()` from Postgres. `SET LOCAL transaction_timeout = '5s'`
- [x] `pkg/outbox`: relay with `SKIP LOCKED`, `LISTEN/NOTIFY` wakeup + 50 ms poll, batch 1000, delete-after-ack, metrics
- [x] `pkg/kafkax`: producer factory (idempotent, acks=all, zstd), header helpers (`ce-type`, `tenant-id`, trace)
- [x] Trace context captured into `outbox.headers` and restored as Kafka headers
- [x] Outbox back-pressure: `RESOURCE_EXHAUSTED` above 5M rows (configurable)
- [x] Fault hook `RELAY_CRASH_AFTER_PRODUCE` (build tag `chaos`)
- [x] gateway route + per-event rate charging + batch size limits
- [x] `make topics` with `rpk` using configs from [04-messaging.md](../04-messaging.md)

## Notes on what was built

- Error codes beyond the sketch: `INVALID_EVENT_TIME`, `IDEMPOTENCY_KEY_INVALID`, `INVALID_PROPERTIES`.
- The fault hook is `outbox.Config.AfterPublish`; only chaos builds wire it to `RELAY_CRASH_AFTER_PRODUCE`
  (`cmd/ingest-svc/chaos.go`, `go build -tags chaos`). Tests set the hook directly.
- `ingest_system` needs `UPDATE (id)` on `outbox` because `FOR UPDATE SKIP LOCKED` requires an UPDATE privilege.
- Outbox depth for back-pressure is an estimate (`max(id) - min(id) + 1`); see [ingest-svc.md](../03-services/ingest-svc.md#back-pressure).
- The Kafka-pause test freezes Redpanda with `docker pause` for 20 s by default; set `ITEST_KAFKA_PAUSE=60s` for the full minute.
- Compose Redpanda Console decodes `usage.events.v1` through the mounted `proto/` directory (`deploy/compose/console/config.yaml`).

## Deliverables

- ingest-svc image (handler + relay in one binary).
- Events visible in Redpanda Console with decoded protobuf (descriptor uploaded).

## Acceptance criteria

- [x] Integration: the same idempotency key sent 3× concurrently → exactly 1 outbox row and 1 Kafka record. Responses: 1 `ACCEPTED`, 2 `DUPLICATE`, all with the same `event_id`
- [x] Integration: same key, different quantity → `REJECTED / IDEMPOTENCY_KEY_REUSED`
- [x] Integration: key accepted on the last day of a month, retried on the 1st of the next → `DUPLICATE` (cross-partition check)
- [x] Integration: Kafka container paused for 60 s while ingesting 10k events → every `ACCEPTED` event appears in Kafka after unpause, count exact
- [x] Integration: `RELAY_CRASH_AFTER_PRODUCE=0.2` → every event in Kafka ≥ 1 time, and duplicates share `event_id`
- [x] Integration: `GetOutboxWatermark` returns the oldest unpublished `ingested_at`, and unset when drained
- [x] Unit: validation table (too old, future, negative quantity, too many decimals, unknown meter/customer, live without key)
- [x] Unit/property: decimal → scaled units → decimal round-trips exactly for every scale 0–6. JSON number `1.1` never goes through `float64`
- [x] Unit: `PeriodOf` for `Europe/Istanbul`, `America/New_York` (DST transitions), `Pacific/Kiritimati` around month boundaries
- [x] Kafka record headers contain `traceparent`, `ce-type=quotify.events.v1.UsageEvent`, `tenant-id`

## Dependencies

Phase 01 (tenants, meters, customers, gateway auth).

## Risks / open questions

- **Decided:** partitioned idempotency table (default). Still benchmark vs. an unpartitioned table with batched `DELETE` and record the numbers in an ADR addendum.
- Relay ordering across concurrent relays is "mostly ordered". Confirm no consumer relies on strict order (by design none should).
- **Decided:** `idempotency_key` is mandatory for live keys, optional in sandbox.
