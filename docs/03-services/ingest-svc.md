# ingest-svc

## Responsibility

Accept usage events **durably and exactly once per idempotency key**, then publish them to Kafka.
It validates, dedups, stamps `event_id`, `ingested_at` and `period_id`, converts the decimal quantity to scaled units,
and writes to the transactional outbox.
It never aggregates.

## gRPC API (sketch)

```proto
package payrudder.ingest.v1;

service IngestService {
  rpc IngestEvents(IngestEventsRequest) returns (IngestEventsResponse);
  // internal, used by billing-svc for the period-close watermark
  rpc GetOutboxWatermark(GetOutboxWatermarkRequest) returns (GetOutboxWatermarkResponse);
}

message IngestEventsRequest { repeated IncomingEvent events = 1; }  // tenant from metadata
message IncomingEvent {
  string idempotency_key = 1;           // 1..128 chars, [A-Za-z0-9_.:-]; required for live keys, optional in sandbox
  string customer_external_id = 2;
  string meter_code = 3;
  string quantity = 4;                  // decimal string, ≥ 0, ≤ meter.quantity_scale decimal places (ADR 0010)
  google.protobuf.Timestamp event_time = 5;
  map<string, string> properties = 6;   // ≤ 32 entries, ≤ 4 KiB total
}
message IngestEventsResponse { repeated EventResult results = 1; } // same order as request
message EventResult {
  enum Status { STATUS_UNSPECIFIED = 0; STATUS_ACCEPTED = 1; STATUS_DUPLICATE = 2; STATUS_REJECTED = 3; }
  Status status = 1;
  string event_id = 2;                  // set for ACCEPTED and DUPLICATE (original id)
  string error_code = 3;                // see the validation table below
  string error_message = 4;
}
message GetOutboxWatermarkResponse {
  google.protobuf.Timestamp oldest_unpublished_ingested_at = 1; // unset = outbox empty
  google.protobuf.Timestamp server_now = 2;
}
```

## Validation rules

| Rule | Error code |
|---|---|
| Meter exists for tenant (cached `GetMeter`) | `UNKNOWN_METER` |
| Customer exists (cached `ResolveCustomer`) | `UNKNOWN_CUSTOMER` |
| `event_time ≥ now − 35 days` (max lateness) | `EVENT_TOO_OLD` |
| `event_time ≤ now + 5 min` (clock skew allowance) | `EVENT_IN_FUTURE` |
| `quantity` is a valid decimal, `≥ 0`, and `quantity × 10^scale` fits in `int64` | `INVALID_QUANTITY` |
| At most `quantity_scale` decimal places (no silent rounding) | `INVALID_QUANTITY_PRECISION` |
| `event_time` present and a valid timestamp | `INVALID_EVENT_TIME` |
| Live key (`qk_live_`) without `idempotency_key`. Sandbox keys get a generated key (`auto:{event_id}`, no dedup) | `IDEMPOTENCY_KEY_REQUIRED` |
| `idempotency_key` is 1..128 characters of `[A-Za-z0-9_.:-]` | `IDEMPOTENCY_KEY_INVALID` |
| At most 32 properties, 4 KiB of names and values in total, no empty names | `INVALID_PROPERTIES` |
| Same idempotency key, **different payload hash** | `IDEMPOTENCY_KEY_REUSED` |

A validation failure rejects that event only. The batch still answers with one result per event, in request order.
Infrastructure failures (plans-svc or Postgres down) fail the whole call with `UNAVAILABLE`, after which nothing has been
written, so a retry is safe. Checks run in this order: key, event time, properties, meter, customer, quantity. The first
failing rule is reported.

The payload hash covers the customer, meter, normalized quantity (`1.0` and `1` are the same), event time and properties
(sorted, length-prefixed). It does not cover the idempotency key itself.

## Derived fields

| Field | Rule |
|---|---|
| `quantity_units`, `quantity_scale` | Exact decimal → scaled `int64` with the meter's scale ([ADR 0010](../adr/0010-money-and-quantity-representation.md)). Never via `float64` |
| `period_id` | `pkg/period.PeriodOf(event_time, tenant.timezone)`: calendar month in the tenant's time zone. The time zone is immutable, so this is stable under replay |
| `ingested_at` | `clock_timestamp()` of the ingest DB, inside the transaction |

Tenant settings (time zone, environment) come from the API-key resolution metadata, so there's no extra lookup.

## Write path (one transaction per request batch)

```sql
BEGIN;
SET LOCAL app.tenant_id = $tenant;
-- 1. claim keys; RETURNING tells us which are new
INSERT INTO idempotency_keys (tenant_id, idempotency_key, event_id, payload_hash, created_at)
SELECT * FROM unnest($tenants, $keys, $event_ids, $hashes, $now)
ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
RETURNING idempotency_key;
-- 2. for keys not returned: SELECT event_id, payload_hash to report DUPLICATE or KEY_REUSED
-- 3. outbox rows only for new keys
INSERT INTO outbox (tenant_id, topic, partition_key, payload, headers, ingested_at) ...;
COMMIT;
```

`ingested_at` is read once per batch with `clock_timestamp()` inside the transaction, before the events are serialized,
because it is part of the payload. `SET LOCAL transaction_timeout = '5s'` bounds the transaction.

Details that differ from the sketch above:

- The outbox insert goes through `enqueue_outbox(...)`, a `SECURITY DEFINER` function that only accepts the caller's own
  tenant. It also issues `pg_notify('outbox_new')`, which Postgres delivers at commit, so the relay wakes up immediately.
  The relay polls every 50 ms as a fallback.
- The keys are claimed in sorted order, so two concurrent batches that share keys can't deadlock.
- A key repeated within one request is resolved in memory: the second occurrence is a `DUPLICATE` of the first, or
  `IDEMPOTENCY_KEY_REUSED` if the payload differs.
- `created_at` of a key row is the start (UTC) of the month, see the partitioned-PK note in
  [05-data-model.md](../05-data-model.md#ingest_db). The two previous months' buckets are checked with an explicit `SELECT` before the insert.
- Sandbox events without a key skip `idempotency_keys` entirely.
- Meter and customer lookups are cached per tenant (hits 30 s, misses 2 s, so a freshly created meter works within
  seconds). A batch resolves its distinct meters and customers concurrently first.

## Outbox relay

- N workers per pod (`pkg/outbox`, default 2): `SELECT id, … FROM outbox ORDER BY id LIMIT 1000 FOR UPDATE SKIP LOCKED`.
  `ingest_system` holds `UPDATE (id)` on the table only because row locking requires some UPDATE privilege; `id` is
  `GENERATED ALWAYS`, so the relay still can't change anything.
- Produce with `franz-go` idempotent producer (`acks=all`, `enable.idempotence`), key = `tenant_id`.
- After the broker acks, `DELETE FROM outbox WHERE id = ANY($ids)` in the same transaction that holds the locks.
- Crash after produce, before delete → rows republished → duplicates downstream, deduped by `event_id`.
  That's why ingest is "at-least-once publish, exactly-once effect".
- The trace context (`traceparent`) captured at ingest is stored in `outbox.headers` and copied into the Kafka record headers.

Details and trade-offs: [07-correctness.md §3](../07-correctness.md#3-transactional-outbox-in-ingest-svc).

## Owned data (`ingest_db`)

`idempotency_keys` (partitioned by `created_at`, monthly, retained 45 days), `outbox`.
See [05-data-model.md](../05-data-model.md#ingest_db).

## Produced messages

`usage.events.v1` (key `tenant_id`, value `payrudder.events.v1.UsageEvent`).

## Consumed messages

None.

## Back-pressure

A background loop refreshes an estimate of the outbox depth every second (`max(id) - min(id) + 1`, two index probes; gaps
left by concurrent relays make it overestimate slightly). Above `PAYRUDDER_MAX_OUTBOX_DEPTH` (default 5,000,000)
`IngestEvents` answers `RESOURCE_EXHAUSTED` and the gateway sets `Retry-After: 5`.

## Partition maintenance

`maintain_idempotency_partitions(now)` (SECURITY DEFINER, executable by `ingest_system` only) creates the partitions of the
current month and the next two, and drops those that ended more than 45 days ago. ingest-svc calls it on start and
every hour (`PAYRUDDER_PARTITION_MAINTENANCE_INTERVAL`); the migration calls it once too. If a month has no partition,
ingest answers `UNAVAILABLE` for keyed events until the job succeeds, so alert on `partition maintenance failed` logs.

## Failure modes

| Failure | Behaviour |
|---|---|
| Kafka down | Ingest keeps accepting (outbox grows). Alert at outbox depth > 100k or age > 60 s. Back-pressure: reject with `RESOURCE_EXHAUSTED` when depth > 5M |
| Postgres down | `UNAVAILABLE`. Nothing acknowledged, so client retries are safe |
| Relay crash mid-batch | Locks released on connection drop, rows retried by another relay |
| Hot tenant | Rate-limited at gateway first. Outbox ordering is global by `id`, so one tenant can delay others by at most one relay batch |

## Scaling notes

The write path is one round trip per batch (`unnest` bulk insert). Expect the bottleneck to be Postgres WAL fsync.
Mitigations, in order: larger client batches, `synchronous_commit=on` kept (correctness), then
partition `idempotency_keys` by hash of tenant. **Target:** 5,000 events/s per pod on a laptop-class Postgres.

## As built: sandbox reset (Phase 45 E)

`SandboxResetService.ResetTenant` (operator only, environment must be SANDBOX) deletes a sandbox tenant's data in ingest_db: dedup keys and unpublished events. plans-svc calls it from the reset job; `internal/sandboxreset` lists what goes and `Kept` what stays on purpose, and a test fails for a table that is neither. See [ADR 0023](../adr/0023-live-and-sandbox-guards.md).
