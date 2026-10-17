# aggregator

## Responsibility

Consume `usage.events.v1` and maintain **authoritative usage aggregates** per
`(tenant, customer, meter, period)` with exactly-once effect. It classifies each event as on-time or
late, serves usage reads, publishes the period watermark, and runs replays (re-aggregation).

It knows **nothing about prices**. Rating happens in billing-svc.

## Consumption model

- Consumer group `aggregator`, `franz-go` with **manual partition assignment callbacks**:
  on assignment, offsets are loaded from `consumer_offsets` in Postgres and the client seeks there.
  Kafka's own committed offset is still committed (for lag dashboards) but never used to resume.
- Per partition: poll → decode → apply batch in **one Postgres transaction**:

```sql
BEGIN;
-- 1. dedup (protects against outbox republish duplicates)
INSERT INTO processed_events (tenant_id, event_id, generation, period_id)
SELECT * FROM unnest(...) ON CONFLICT DO NOTHING
RETURNING event_id;
-- 2. apply only the returned (new) events, pre-summed in Go per key
INSERT INTO usage_aggregates (tenant_id, customer_id, meter_code, period_id, generation, quantity, event_count, updated_at)
VALUES ... ON CONFLICT (tenant_id, customer_id, meter_code, period_id, generation)
DO UPDATE SET quantity = <SUM/MAX merge>, event_count = usage_aggregates.event_count + EXCLUDED.event_count;
-- late events go to late_usage instead (see below)
-- 3. advance the offset in the same transaction
INSERT INTO consumer_offsets (consumer_group, topic, partition, next_offset)
VALUES ('aggregator', 'usage.events.v1', $p, $m + 1)
ON CONFLICT (consumer_group, topic, partition) DO UPDATE SET next_offset = EXCLUDED.next_offset
WHERE consumer_offsets.next_offset <= EXCLUDED.next_offset;   -- fencing: never move backwards
COMMIT;
```

Why both the dedup table and offsets: [07-correctness.md §2](../07-correctness.md#2-exactly-once-like-processing).

RLS: the consumer runs as role `aggregator_system` (BYPASSRLS) because one batch spans tenants.
Read APIs run as `aggregator_app` with `SET LOCAL app.tenant_id`.

## On-time vs late classification

```
period  = calendar_month_utc(event.event_time)
cutoff  = period.end + grace(tenant, period)     -- grace snapshotted in period_settings
if event.ingested_at < cutoff → usage_aggregates (period)
else                          → late_usage (origin_period = period, billed_in_period = month(ingested_at))
```

This depends only on event fields plus snapshotted config, so replaying the log always reproduces it.
Grace is copied into `period_settings` when the first event of a tenant's period arrives. Later
changes to the tenant setting affect only future periods.

## gRPC API (sketch)

```proto
package quotify.aggregator.v1;

service UsageQueryService {
  rpc GetCustomerUsage(GetCustomerUsageRequest) returns (GetCustomerUsageResponse);   // customer, period → per-meter qty
  rpc GetUsageBatch(GetUsageBatchRequest) returns (GetUsageBatchResponse);            // limits reconciliation, ≤ 1000 keys
  rpc StreamPeriodUsage(StreamPeriodUsageRequest) returns (stream UsageAggregate);    // billing: all customers of tenant+period
  rpc StreamLateUsage(StreamLateUsageRequest) returns (stream LateUsage);             // billing: late usage billed in period
  rpc GetIngestWatermark(GetIngestWatermarkRequest) returns (GetIngestWatermarkResponse);
  rpc FreezePeriod(FreezePeriodRequest) returns (FreezePeriodResponse);              // billing, after rating; idempotent
}

service ReplayService {                                                               // operator only
  rpc StartReplay(StartReplayRequest) returns (ReplayJob);   // tenant, period range, reason
  rpc GetReplayJob(GetReplayJobRequest) returns (ReplayJob);
  rpc PromoteGeneration(PromoteGenerationRequest) returns (ReplayJob); // atomic switch after verification
}

message GetIngestWatermarkRequest {
  map<int32, int64> required_offsets = 1;  // partition → end offset snapshot taken by billing-svc
}
message GetIngestWatermarkResponse {
  bool caught_up = 1;                       // every partition next_offset ≥ required
  map<int32, int64> committed_offsets = 2;
}
```

## Owned data (`aggregator_db`)

`usage_aggregates`, `late_usage`, `processed_events` (partitioned by `period_id`), `consumer_offsets`,
`period_settings`, `aggregate_generations`, `replay_jobs`. See [05-data-model.md](../05-data-model.md#aggregator_db).

## Consumed / produced messages

| Direction | Topic | Notes |
|---|---|---|
| in | `usage.events.v1` | group `aggregator` |
| in | `usage.events.v1` | group `aggregator-replay-{job_id}` during replay, seeking by timestamp |
| out | `usage.events.dlq.v1` | undecodable or invalid records, with error headers |

## Failure modes

| Failure | Behaviour |
|---|---|
| Crash after COMMIT, before Kafka commit | On restart, seek to `consumer_offsets` → no reprocessing |
| Crash before COMMIT | Transaction rolled back, batch re-read. Nothing applied twice |
| Rebalance mid-batch (zombie consumer) | Fencing `WHERE next_offset <= EXCLUDED.next_offset` plus dedup table. A zombie's late commit either no-ops or touches only already-processed IDs |
| Poison record | After 3 local decode/validate failures → DLQ with headers, offset advances in the same transaction |
| Postgres slow | Consumer lag grows, period close waits on the watermark. Nothing incorrect |

## Scaling notes

Parallelism = partitions (12). Batch size 500–2000 records per transaction. Pre-summing in Go collapses
N events to ≤ N distinct keys, which is a large win for hot customers. `processed_events` is the largest table.
Monthly partitions are dropped 45 days after period end. **Target:** 20k events/s across 12 partitions.
