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
DO UPDATE SET quantity = <SUM/MAX/LAST merge>, event_count = usage_aggregates.event_count + EXCLUDED.event_count;
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
  generation, `ON CONFLICT DO NOTHING RETURNING`), classifies and pre-sums only the returned events, upserts SUM/COUNT, MAX and LAST rows in one
  statement per merge with keys in a fixed order (UNIQUE meters first settle their value sets, see below), and advances the offset with the fencing `WHERE`. A no-op offset write means a zombie and is counted
  (`aggregator_fenced_batches`).
- A meter's `filter` is applied at aggregation: a non-matching event is marked processed and ignored (`aggregator_filtered_events`).
  The quantity is the event's validated `quantity_units` (1 for COUNT, none for UNIQUE, which counts the distinct values of the meter's `value_property`); for the other aggregations `value_property` isn't used.
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

`usage_aggregates`, `usage_window_aggregates`, `usage_distinct_values`, `usage_distinct_hours`, `late_usage`, `processed_events` (partitioned by `period_id`),
`consumer_offsets`, `period_settings`, `aggregate_generations`, `replay_jobs`. See [05-data-model.md](../05-data-model.md#aggregator_db).

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
whole), the value of the latest event (`aggregation = AGGREGATION_LAST`) or the number of distinct values (`AGGREGATION_UNIQUE`, see [UNIQUE and LAST](#as-built-unique-and-last)). The same snapshot returns the consumer offsets, so the checkpoint protocol of limits-svc is unchanged.

- **Month, year, lifetime** read `usage_aggregates`: a year is the twelve month rows of the tenant's calendar year, the lifetime window every month row there
  is. These rows are kept for good and are what billing rates, so these windows agree with the invoice by construction.
- **Hour, day, ISO week** read `usage_window_aggregates`, a total per customer, meter and wall-clock hour (`2026-11-05T13`), written by the
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

## As built: UNIQUE and LAST

Two aggregations whose parts do not add up ([ADR 0025](../adr/0025-unique-and-last-aggregations.md)). A meter's `aggregation` is `SUM`, `COUNT`, `MAX`, `UNIQUE` or `LAST`;
the first three keep one number per row and merge by adding or keeping a maximum, these two keep a little more and merge by a law that does not depend on arrival order.

### LAST: the quantity of the latest event, in a total order

- **The order** (`pkg/usage.Order`): the event time truncated to the microsecond (what the wire and `timestamptz` keep), then the event id compared **bytewise**. Event ids are
  ULIDs assigned once at ingest, so two different events never compare equal, and the latest event of a set does not depend on the order in which partitions, redeliveries or a
  replay deliver it. Postgres compares with `COLLATE "C"`, Lua with its own byte loop (the `<` of Redis strings follows the server's locale), Go with `<` on strings; one test
  feeds all three the same ties.
- **Storage**: `usage_aggregates.quantity` is the quantity of the winning event, and `last_at` / `last_event_id` (NULL for every other aggregation) name it. The upsert
  keeps the incoming `(quantity, last_at, last_event_id)` only if `(last_at, last_event_id COLLATE "C")` is greater than the stored one. The hour rows (`usage_window_aggregates`) carry the same
  columns, so a day, a week, a year or the lifetime window is the winner among its rows (`array_agg(... ORDER BY last_at DESC, last_event_id COLLATE "C" DESC)`), and the answer names the event
  (`UsageAggregate.last_event_time` / `last_event_id`) so that limits-svc repairs its counter to that event and not only to its value.
- **The merge laws** (`domain.Merge`, property-tested): associative, commutative and idempotent. A delivery order, a batch split or a replay of the same events gives the same row.
- **A late event never changes a closed period.** The last value of a period is the last among the events ingested before its cutoff. A later event is stored nowhere (the batch counts it in
  `aggregator_late_last_events`) and is not billed: a "last value" cannot be added to an invoice that was already issued the way a sum can, and a correction would mean re-rating the
  period with a different answer. The consequence is stated plainly: a LAST meter whose events arrive after the cutoff (buffered SDKs, backfills) is wrong by exactly those events,
  and the tenant's grace period is the knob. A replay reproduces the rule, it does not undo it.

### UNIQUE: the number of distinct values of a property

- **Why a set.** A distinct count is not additive: a customer seen in two months is one customer, and a day or a year cannot be read from the counts of its hours or months.
  The aggregator therefore keeps the values themselves (`usage_distinct_values`, one row per customer, meter, generation, period and **SHA-256 of the value**; `usage_distinct_hours` per
  wall-clock hour for the short quota windows). A property can be 4 KiB and a primary key cannot, so the digest is the identity; two different values collide with a probability that is
  not a consideration (2⁻¹²⁸ per pair at the birthday bound). Billing reads this table's counts, so billing is exact up to SHA-256 collision resistance and nothing approximate (no HyperLogLog).
- **The state of a value in a period** is `billed_period_id`: `''` if an on-time event carried it, else the earliest billing period of a late event that carried it. `''` sorts before every
  period id, so the state is the **minimum of what its events say** (`domain.MergeSeen`): associative, commutative, idempotent. An on-time event after a late one of the same value moves it
  to `''`, an earlier billing period after a later one moves it earlier, and the order of arrival does not matter.
- **The counts follow the states, in one transaction.** `usage_aggregates.quantity` is the number of values in state `''` and `late_usage(billed, origin).quantity` the number in state
  `billed`. A batch (1) claims its `(period, value)` pairs with `INSERT ... ON CONFLICT DO NOTHING RETURNING` in key order, (2) lowers the state of the pairs that exist with
  `UPDATE ... FROM (SELECT ... FOR UPDATE)` (only where the new state is smaller; the row lock serialises two consumers that carry the same value), (3) adds `+1` to the row of every new or
  new-state value and `-1` to the row it left, then upserts the counts as plain sums next to the event counts. The dedup inbox, the generations (every target generation gets its own
  sets) and the meter filter apply first, exactly as for the other aggregations. An event without the property counts nothing (`aggregator_missing_value_events`); ingest rejects such an
  event for a UNIQUE meter, so this only guards against meters changed under in-flight events.
- **Windows.** A single billing period is read from the stored count. A year or the lifetime window is `count(DISTINCT value_hash)` over the on-time rows of its months, an hour from the hour
  set, a day or a week from the hour sets of its range, so a value that appears on two days counts once in the week. `UsageKey.aggregation = AGGREGATION_UNIQUE` selects this; for such a
  key `event_count` in the answer is the number of values. The wall-clock hour id keeps daylight saving harmless, as for the sums.
- **Late and closed periods.** A value first seen after the cutoff is late usage billed in the period of its first late event; a value an on-time event carried is not billed again. Rating
  prices the late count incrementally on top of the on-time count and the late lines already invoiced (`rate(on-time + prior + late) − rate(on-time + prior)`), which is exact because the
  counts are disjoint by construction. One case is not retroactive: if an on-time event of a value arrives **after** a late event of it was processed *and that late line was already
  invoiced*, the unit moves back to the period and `late_usage` drops, but the invoice is not corrected (the same hole as an on-time event for a frozen period, and as visible: the
  conversion happens only when the two events are processed out of ingest order across the cutoff, a window of seconds).
- **Cost.** One set row per distinct value per customer, meter and period (about 100 bytes with its index entry), kept as long as the month rows (forever), and per hour for 14 days. A
  UNIQUE meter on a very high-cardinality property (millions of values per customer) is the expensive case; a year or lifetime read scans the rows of the customer and meter. There is no cap
  here, because a cap would make billing inexact; the plans-svc tier quotas bound the number of meters, not values.

### Replay, retention, reset

The new tables follow the rows they explain: a replay clears and carries over `usage_distinct_values` / `usage_distinct_hours` for the periods outside its range like the month and hour
rows, a discarded generation and a sandbox reset delete them, and maintenance prunes the hour sets with the hour totals. Replay verification compares the counts and quantities of
`usage_aggregates` and `late_usage` (a UNIQUE count or a LAST value), not the sets.

## In a cell (phase 28)

With `PAYRUDDER_CELLS` set ([runbook](../runbooks/cells.md)) a tenant that the directory places in a cell has its nine tables in its own schema `t_<id>` of the cell's `aggregator_db` (`migrations/tenant`: the public migrations written for a schema, the inbox partitions kept by functions that bind the schema they live in; `TestTenantSchemaMatchesPublic` compares the two). `consumer_offsets` is not a tenant's, so it stays in the pooled database and holds the position of the whole group. The query and period APIs open their transactions through the router, so the same statements run in the tenant's schema; a tenant that is being moved is answered `Unavailable`.

**The usage consumer** splits a batch by placement. Every tenant of the batch is located first (a tenant that is moving, or whose cell this process has no connection to, stops the batch before anything is written: the partition waits and is retried, no tenant is skipped). Then each tenant placed in a cell is written in transactions of its own in its own schema, in ascending tenant order: the period snapshots and inbox partitions, then dedup, classification, the upserts, the UNIQUE value sets and the hour totals, all under that tenant's inbox. A tenant whose transaction fails does not keep the others from becoming durable, and its own rows are rolled back whole; the failures are joined. Only when every cell tenant is durable does the pooled transaction run, which writes the pooled tenants as before and moves the offset in the same transaction. So the offset passes a record only after all of its splits are durable, and a batch delivered again after a partial failure or a crash between the splits and the offset finds the durable splits already in their inbox and counts them once (`TestConsumerBatchWithAFailureInTheMiddle`, `TestConsumerCrashBetweenTheSplitsAndTheOffset`). Without cells the batch is the single transaction above, unchanged. A replay handler is the same code with one tenant, so it writes that tenant's cell in one transaction and its own offsets in the pooled one. While a tenant moves (offline, seconds to minutes) the partitions that carry its events wait.

**Reads.** `GetUsageBatch` returns usage and the consumer offsets "as of" one snapshot. For a tenant in the pooled database that is still one transaction. For a tenant in a cell the offsets (pooled database) are read first and the usage (the cell) after: the consumer moves an offset only after the usage below it is durable, so the usage read holds at least everything below the offsets it is paired with (possibly more), which is the direction the billing and limits checks need.

**Replay and promote** run per placement: the manager's scan looks at the pooled database and at every serving tenant's schema (a job of a tenant that is moving, or whose pooled row the directory has since placed elsewhere, is not started; a running job is never started twice), and every step of a job (history, verification, promote, discard, carry-over of the periods outside the range) runs in the tenant's schema. One step differs: the pooled consumer's position and the tenant's generation rows are in different databases, so the position at which dual writing began cannot be read in the flip's transaction. The flip in a cell records the end of the log instead, read while it holds the tenant's active generation; every batch that wrote only the old generation committed before that, so its events are below it, and the replay reads up to there (events the live consumer dual-writes as well are skipped by the per-generation inbox). A job interrupted by a move is not counted as a failed attempt.

**Maintenance** (inbox partitions older than 45 days, hour totals and hour sets older than 14 days) and the **sandbox reset** visit the pooled database and each serving tenant of the cells once, in the tenant's own schema.
