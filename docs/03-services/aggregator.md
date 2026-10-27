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
-- (one row per target generation: the tenant's active one, plus its catching_up one during a promotion)
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

### As built (Phase 03)

- `pkg/kafkax.Consumer` loads the offsets in `AdjustFetchOffsetsFn` (on every assignment) from `consumer_offsets`, applies the partitions of a poll
  concurrently and holds rebalances off while a batch is in flight (`BlockRebalanceOnPoll`). A failed batch rewinds the partition to its
  database offset and pauses it for a second, which is also what a restart does. Kafka commits are advisory marks.
- Before the transaction, outside it: records are decoded (undecodable or invalid ones are poison), meters are resolved (a meter plans-svc
  doesn't know is retried at 100 ms / 500 ms / 2 s before the record goes to the DLQ; a plans-svc outage pauses the partition instead),
  poison records are sent to the DLQ (`x-dlq-*` and `x-original-*` headers), `period_settings` snapshots and inbox partitions are ensured.
  Deterministic decode and validation failures skip the in-process retries.
- The transaction reads `period_settings` and the tenant's generation rows `FOR SHARE`, inserts the inbox rows (one per event and target
  generation, `ON CONFLICT DO NOTHING RETURNING`), classifies and pre-sums only the returned events, upserts SUM/COUNT and MAX rows in two
  statements with keys in a fixed order, and advances the offset with the fencing `WHERE`. A no-op offset write means a zombie and is counted
  (`aggregator_fenced_batches`).
- A meter's `filter` is applied at aggregation: a non-matching event is marked processed and ignored (`aggregator_filtered_events`).
  The quantity is the event's validated `quantity_units` (1 for COUNT); the meter's `value_property` isn't used.
- A frozen period turns every later arrival into late usage; one that would have been on time counts in
  `aggregator_frozen_period_violations` and logs an error (the suite asserts it stays 0).
- Fault hook: `AGG_CRASH_AFTER_COMMIT` (chaos build tag) exits the process after the commit. In-process tests set `Handler.AfterCommit`.

## On-time vs late classification

```
period  = event.period_id                         -- stamped at ingest: calendar month in the tenant's time zone
cutoff  = period_settings(tenant, period).cutoff_at   -- period_end + grace, snapshotted
if event.ingested_at < cutoff → usage_aggregates (period)
else                          → late_usage (origin_period = period,
                                            billed_in_period = PeriodOf(ingested_at, tenant.timezone))
```

This depends only on event fields plus snapshotted config, so replaying the log always reproduces it.
Time zone, period bounds and grace are copied into `period_settings` when the first event of a tenant's period arrives
(settings fetched from plans-svc, cached). The time zone is immutable. A grace change affects only future periods.
Quantities are already scaled integers (`quantity_units`), so the upsert is plain integer arithmetic.

### Sandbox early close

Sandbox tenants can close the current period on demand to try the whole invoice and payment flow without waiting a month
([ADR 0011](../adr/0011-sandbox-environment.md)). billing-svc calls `ClosePeriodEarly(tenant, period)`, which runs:

```sql
UPDATE period_settings
SET cutoff_at = clock_timestamp() + interval '5 seconds', early_closed_at = clock_timestamp()
WHERE tenant_id = $1 AND period_id = $2 AND frozen_at IS NULL AND cutoff_at > clock_timestamp() + interval '5 seconds'
RETURNING cutoff_at;
```

The live batch transaction reads the tenant's `period_settings` row `FOR SHARE`, so the update waits for in-flight batches and
every batch after it sees the new cutoff. The 5 s margin absorbs clock skew between the ingest and aggregator databases.
Events ingested after the new cutoff go to `late_usage` and are billed on the next sandbox invoice. The new cutoff is recorded,
so replays stay deterministic. Rejected with `FAILED_PRECONDITION` for live tenants (checked from the tenant's environment, not from a flag in the request).

## gRPC API (sketch)

```proto
package payrudder.aggregator.v1;

service UsageQueryService {
  rpc GetCustomerUsage(GetCustomerUsageRequest) returns (GetCustomerUsageResponse);   // customer, period → per-meter qty
  rpc GetUsageBatch(GetUsageBatchRequest) returns (GetUsageBatchResponse);            // limits reconciliation, ≤ 1000 keys; a key names a period or a quota window
  rpc StreamPeriodUsage(StreamPeriodUsageRequest) returns (stream StreamPeriodUsageResponse);    // billing: all customers of tenant+period
  rpc StreamLateUsage(StreamLateUsageRequest) returns (stream StreamLateUsageResponse);             // billing: late usage billed in period
  rpc GetIngestWatermark(GetIngestWatermarkRequest) returns (GetIngestWatermarkResponse);
  rpc FreezePeriod(FreezePeriodRequest) returns (FreezePeriodResponse);              // billing, after rating; idempotent
  rpc ClosePeriodEarly(ClosePeriodEarlyRequest) returns (ClosePeriodEarlyResponse);  // billing, sandbox tenants only
}

service ReplayService {                                                               // operator only
  rpc StartReplay(StartReplayRequest) returns (ReplayJob);   // one tenant (required), period range, reason
  rpc GetReplayJob(GetReplayJobRequest) returns (ReplayJob);
  rpc PromoteGeneration(PromoteGenerationRequest) returns (ReplayJob); // per-tenant switch after verification
  rpc DiscardGeneration(DiscardGenerationRequest) returns (ReplayJob); // abandon a building generation
}

message GetIngestWatermarkRequest {
  map<int32, int64> required_offsets = 1;  // partition → end offset snapshot taken by billing-svc
}
message GetIngestWatermarkResponse {
  bool caught_up = 1;                       // every partition next_offset ≥ required
  map<int32, int64> committed_offsets = 2;
}
```

## Per-tenant generations

Each tenant has its own active generation ([ADR 0012](../adr/0012-per-tenant-aggregate-generations.md)). A replay for tenant T
never touches other tenants' rows, and promotion flips only T. Live traffic is not paused.

1. `StartReplay(T, from, to)` creates `(T, N+1, building)` and a replay consumer group `aggregator-replay-{job}` that reads from
   `ListOffsetsAfterMilli(period_start(from) − 35 d)` and skips records of other tenants or periods.
2. When it reaches the end-offset snapshot, the job moves to `catching_up`. From then on the **live** consumer writes T's events
   to **both** N and N+1, and records per partition the first offset it dual-wrote (`replay_jobs.dual_write_from`).
3. The replay consumer keeps going until it reaches `dual_write_from` on every partition. Overlap between the two consumers is
   harmless: `processed_events` is keyed by generation, so each event is applied to N+1 exactly once.
4. Verification diff N vs N+1 → `ready`. `PromoteGeneration` flips T's `active` row in one transaction. The live consumer
   picks this up in its next batch (generation rows are read `FOR SHARE` in the batch transaction) and stops dual-writing.
5. Billing runs pinned to N keep reading N. Retired generations are dropped after 90 days.

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

## As built: sandbox reset (Phase 45 E)

`SandboxResetService.ResetTenant` (operator only, environment must be SANDBOX) deletes a sandbox tenant's data in aggregator_db: usage, late usage, processed events and period settings (generations and replay jobs stay). plans-svc calls it from the reset job; `internal/sandboxreset` lists what goes and `Kept` what stays on purpose, and a test fails for a table that is neither. See [ADR 0023](../adr/0023-live-and-sandbox-guards.md).

## As built: window totals (quota windows)

limits-svc counts a quota over a window (an hour, a day, an ISO week, the billing month, a calendar year or the customer's lifetime, all in the tenant's
time zone) and reconciles the counter of **every** window against this service, so that enforcement, the console and the invoice are made from the same events.
`GetUsageBatch` takes a window id in `UsageKey.period_id` (`2026-11`, `2026-11-05T13`, `2026-11-05`, `2026-W45`, `2026`, `all`; `pkg/period.Span` maps an id to
the stored rows) and answers with the sum of those rows, or their maximum when the key says `max` (a MAX meter: the maximum of the parts is the maximum of the
whole). The same snapshot returns the consumer offsets, so the checkpoint protocol of limits-svc is unchanged.

- **Month, year, lifetime** read `usage_aggregates`: a year is the twelve month rows of the tenant's calendar year, the lifetime window every month row there
  is. These rows are kept for good and are what billing rates, so these windows agree with the invoice by construction.
- **Hour, day, ISO week** read `usage_window_aggregates` (migration 00004), a total per customer, meter and wall-clock hour (`2026-11-05T13`), written by the
  batch transaction next to the month row: the same dedup, meter filter and on-time rule (a late or frozen-period event is in neither), the hour taken from
  `event_time` in the time zone of the period snapshot. A day is the hours of that date and a week the hours from Monday 00 to Sunday 23, as a range of the
  sortable hour id, so no time zone is needed to read it. The wall-clock id is what makes daylight saving harmless: the repeated hour of a fall-back day is one id
  (counted once, as limits-svc counts it) and the skipped hour of a spring-forward day has none.
- **A rolling window, not a ledger.** Hour rows older than 14 days (`domain.WindowRetention`) are not written and are deleted by the hourly maintenance;
  billing never reads them and replay verification does not compare them. A replay rebuilds those of the range it replays and carries the others over to the new
  generation like the month rows. A sandbox reset and a discarded generation delete them. An hour, day or week older than the retention reads as zero, which is
  why limits-svc only reconciles windows that are current or ended less than two hours ago.
- **Cost:** one more upserted row per (customer, meter, hour) of a batch, in the same transaction and in a fixed key order after the month rows. It is
  written for every meter, because this service does not know which meters have a short quota.
- **Why hour totals.** Day and week windows cannot be derived from the month row. A table per entitlement window would need this service to follow
  `plans.entitlements.v1` and to backfill when an entitlement appears. Rebuilding from the Kafka log is bounded by its retention and slow. One finest grain
  that every short window is a range of needs neither.
