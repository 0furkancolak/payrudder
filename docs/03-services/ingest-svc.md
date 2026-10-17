# ingest-svc

## Responsibility

Accept usage events **durably and exactly once per idempotency key**, then publish them to Kafka.
It validates, dedups, stamps `event_id` and `ingested_at`, and writes to the transactional outbox.
It never aggregates.

## gRPC API (sketch)

```proto
package quotify.ingest.v1;

service IngestService {
  rpc IngestEvents(IngestEventsRequest) returns (IngestEventsResponse);
  // internal, used by billing-svc for the period-close watermark
  rpc GetOutboxWatermark(GetOutboxWatermarkRequest) returns (GetOutboxWatermarkResponse);
}

message IngestEventsRequest { repeated IncomingEvent events = 1; }  // tenant from metadata
message IncomingEvent {
  string idempotency_key = 1;           // 1..128 chars, [A-Za-z0-9_.:-]
  string customer_external_id = 2;
  string meter_code = 3;
  int64 quantity = 4;                   // ≥ 0, base units of the meter
  google.protobuf.Timestamp event_time = 5;
  map<string, string> properties = 6;   // ≤ 32 entries, ≤ 4 KiB total
}
message IngestEventsResponse { repeated EventResult results = 1; } // same order as request
message EventResult {
  enum Status { STATUS_UNSPECIFIED = 0; ACCEPTED = 1; DUPLICATE = 2; REJECTED = 3; }
  Status status = 1;
  string event_id = 2;                  // set for ACCEPTED and DUPLICATE (original id)
  string error_code = 3;                // UNKNOWN_METER, UNKNOWN_CUSTOMER, EVENT_TOO_OLD, EVENT_IN_FUTURE, INVALID_QUANTITY, IDEMPOTENCY_KEY_REUSED
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
| `quantity ≥ 0` | `INVALID_QUANTITY` |
| Same idempotency key, **different payload hash** | `IDEMPOTENCY_KEY_REUSED` |

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

Then `NOTIFY outbox_new` so the relay wakes immediately. The relay also polls every 50 ms as a fallback.

## Outbox relay

- N workers per pod: `SELECT id, … FROM outbox ORDER BY id LIMIT 1000 FOR UPDATE SKIP LOCKED`.
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

`usage.events.v1` (key `tenant_id`, value `quotify.events.v1.UsageEvent`).

## Consumed messages

None.

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
