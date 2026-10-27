# limits-svc

## Responsibility

Near-real-time entitlement state. It keeps Redis counters of usage per `(tenant, customer, meter, period)`,
answers `CheckEntitlement`, emits `usage.limit-exceeded.v1` when a threshold is crossed, and reconciles
its counters against aggregator's authoritative totals.

**Consistency model:** eventually consistent, with bounded staleness (**target:** p99 < 2 s from ingest
to counter). It is not the billing source of truth. See [ADR 0006](../adr/0006-eventual-consistency-for-limits.md).

## Inputs

| Topic | Group | Use |
|---|---|---|
| `usage.events.v1` | `limits` | increment counters |
| `plans.entitlements.v1` (compacted) | `limits-entitlements-{pod}` (each pod reads all partitions from the beginning) | in-memory map of limits + Redis mirror `lim:ent:{tenant}:{customer}:{meter}` |

## Redis keys

| Key | Type | TTL |
|---|---|---|
| `lim:c:{tenant}:{customer}:{meter}:{period}` | string int (counter, scaled units of the meter) | period end + 40 days |
| `lim:off:{partition}` | string int (next offset applied) | none |
| `lim:fired:{tenant}:{customer}:{meter}:{period}:{pct}` | string `1` | period end + 40 days |
| `lim:w:{tenant}:{customer}:{meter}:{window}` | string int (counter of an hour, day or ISO week window) | window end + 2 days |
| `lim:y:{tenant}:{customer}:{meter}:{window}` | string int (counter of a calendar year `2026` or the lifetime `all`) | year end + 2 days; the lifetime counter never expires |
| `lim:wactive:{partition}` | sorted set of the short-window counters (`lim:w`; score = last update) | entries pruned after 1 h |
| `lim:set:{counter}` | set of 128-bit value digests behind a `UNIQUE` counter (`{counter}` = the counter key without `lim:`) | the counter's |
| `lim:ord:{counter}` | `micros:event_id` of the event a `LAST` counter is the value of | the counter's |

All counter keys of a batch for partition `p` are updated in **one Lua script** together with
`lim:off:{p}`. That works because the script runs atomically on a single Redis node. In Redis Cluster the keys
would need a shared hash tag `{p}`. Locally and in kind we run a single node, so this is noted, not built.

```lua
-- apply_batch.lua  (sketch)
-- KEYS[1] = lim:off:{p}; ARGV[1] = first_offset, ARGV[2] = next_offset, then triples (counter_key, delta, limit)
local applied = tonumber(redis.call('GET', KEYS[1]) or '0')
if tonumber(ARGV[1]) < applied then return {'SKIP'} end      -- batch already applied (redelivery)
local crossed = {}
for i = 3, #ARGV, 3 do
  local before = tonumber(redis.call('GET', ARGV[i]) or '0')
  local after  = redis.call('INCRBY', ARGV[i], ARGV[i+1])
  -- thresholds evaluated in Go from (before, after, limit); script returns before/after pairs
  table.insert(crossed, {ARGV[i], before, after})
end
redis.call('SET', KEYS[1], ARGV[2])
return crossed
```

Counters hold scaled integer units ([ADR 0010](../adr/0010-money-and-quantity-representation.md)), so `INCRBY` stays exact for
fractional meters. Lua's `tonumber` is a double, so the script compares values only below 2⁵³ and Go rejects a delta that would
push a counter past that (logged, counted in `limits_counter_overflow_total`, and the key is left for reconciliation).

Late events (classified with the same function as aggregator, shared package `pkg/period`) are
**not** counted against the current period's limit. They count toward their origin period, which is usually closed.

## Limit-exceeded events

For each threshold `t` in the entitlement's `alert_thresholds_pct` where `before < t% × limit ≤ after`:

1. `SET lim:fired:…:{t} 1 NX`. If the key already exists, skip (another pod or a retry already emitted).
2. Produce `LimitExceeded` with **deterministic** `event_id = uuidv5(tenant|customer|meter|period|t)`, so
   consumers can dedup even if step 1 is lost through a Redis failover.

## Reconciliation

Every 5 min (configurable) per pod, for its assigned partitions:

1. Collect active counter keys from a sorted set `lim:active:{p}` (score = last update): the billing-period counters and the year and lifetime
   counters, plus the short-window counters (`lim:wactive:{p}`) updated in the last hour whose window has not ended more than two hours ago.
2. Call aggregator `GetUsageBatch(keys, partition=p)` (a key is a period or a window id, see [aggregator.md](aggregator.md#as-built-window-totals-quota-windows)). Aggregator reads the totals **and** its
   `consumer_offsets.next_offset` for `p` in one `REPEATABLE READ` transaction. The result is a consistent
   snapshot `(totals, O_a)`: "these totals contain exactly the events of `p` before offset `O_a`".
3. Compare with limits' own applied offset `O_l = lim:off:{p}`:
   - `O_l > O_a` (limits ahead): skip `p` this cycle (`limits_reconcile_skipped_total{reason="ahead"}`).
   - `O_l ≤ O_a`: register a **checkpoint** at `O_a` with the partition's apply loop. The loop is the single
     goroutine owning `p`. It splits the next batch at `O_a`, applies records `< O_a`, then `MGET`s the
     checkpointed counters *before* applying the rest. Now both sides reflect exactly the same prefix of `p`.
     Checkpoints that aren't reached within 30 s are dropped.
4. `drift = redis − aggregator` per key. If `drift ≠ 0` → `INCRBY counter -drift` inside the apply loop
   (still serial, so no lost updates), increment `limits_reconcile_corrections_total{reason}`, and log at WARN with the key.

No pausing, no locks between services. Correctness rests on the two snapshots describing the same
offset prefix of the same partition.

Sources of drift this repairs: outbox duplicates (limits doesn't dedup by `event_id`), Redis data loss on
failover, and bugs.

## gRPC API (sketch)

```proto
package payrudder.limits.v1;

service LimitsService {
  rpc CheckEntitlement(CheckEntitlementRequest) returns (CheckEntitlementResponse);
  rpc GetLimitStatus(GetLimitStatusRequest) returns (GetLimitStatusResponse);   // all meters for a customer
  rpc ReconcileNow(ReconcileNowRequest) returns (ReconcileNowResponse);         // operator
}
message CheckEntitlementResponse {
  int64 used = 1;
  int64 limit = 2;            // 0 = unlimited
  bool allowed = 3;           // used < limit
  google.protobuf.Timestamp as_of = 4;   // time of last applied event, staleness visible to caller
}
```

Payrudder **does not block ingest** when a limit is exceeded. Usage events are facts that already happened.
Tenants call `CheckEntitlement` before serving their customer and decide what to do.

## Failure modes

| Failure | Behaviour |
|---|---|
| Redis restart without persistence | Counters reset → reconciliation restores them within one cycle. `CheckEntitlement` returns low numbers until then (documented risk). AOF `everysec` enabled to shrink this window |
| limits-svc lag | `as_of` shows staleness. Alert at lag > 10 s |
| Duplicate event (outbox republish) | Counter over-counts by that event until next reconciliation. Not for `UNIQUE` (its set absorbs the duplicate), `LAST` or `MAX` (idempotent) |
| Threshold crossed by two pods simultaneously | Impossible per key: a partition has one owner, and `SET NX` covers handover races |

## Scaling notes

One Lua call per batch per partition. Parallelism = partitions. Redis single-node ceiling is far above
our ingest **targets**. `CheckEntitlement` is a single `MGET` (counter + entitlement).

## As built (Phase 04)

- Code: `services/limits-svc` (`internal/engine` = Lua apply, thresholds, checkpoints, reconciler; `internal/entitlements` =
  in-memory map + Redis mirror; `internal/query` = gRPC). Configuration is environment-only (`PAYRUDDER_REDIS_URL`,
  `PAYRUDDER_KAFKA_BROKERS`, `PAYRUDDER_PLANS_ADDR`, `PAYRUDDER_AGGREGATOR_ADDR`, `PAYRUDDER_RECONCILE_INTERVAL`, `PAYRUDDER_UNIQUE_SET_CAP`, `PAYRUDDER_POD_NAME`).
- The script takes `(key, op, delta, expireat)` per counter (`op` `I` increments, `M` keeps the maximum for `MAX` meters),
  writes `lim:off:{p}`, `lim:active:{p}` and `lim:ts:{tenant}` (the `as_of` of `CheckEntitlement`) in the same call, and
  refuses to pass 2^53 (`limits_counter_overflow`).
- `CheckEntitlement` answers from the in-memory entitlement and one `MGET` (counter + `as_of`). The readiness probe fails
  until the entitlement map has read the topic up to the end offsets recorded at start.
- Metrics: `limits_events_applied`, `limits_events_skipped{reason}`, `limits_reconcile_corrections{reason}`,
  `limits_reconcile_skipped{reason}`, `limits_reconcile_keys_checked`, `limits_thresholds_fired`.

## As built: quota windows and modes

- An entitlement counts over a **window** in the tenant's time zone (`pkg/period.WindowOf`): the billing month (`lim:c`, as before), an hour, a day or an
  ISO week (`lim:w`; ids `2026-11-05T13`, `2026-11-05`, `2026-W45`), a calendar year or the customer's lifetime (`lim:y`; ids `2026` and `all`). A year is
  January 1st 00:00 to the next, in the tenant's zone, so it rolls over at the tenant's midnight and not at UTC's; the lifetime window has no start and no end
  (`WindowOf` returns the zero time for both). For an entitlement with a window other than the month the apply loop adds the event to the counter of
  `event_time`'s window as well, in the same script call; the month counter keeps counting everything.
- Window counters are kept **only while an entitlement asks for them**. A counter starts with the first event after limits-svc learns the entitlement and is
  brought to the aggregator's total of the window by the next reconciliation (within one interval), so switching a meter from monthly to daily, yearly or
  lifetime counts what the window held before the entitlement existed, too. Until then, and for a customer with no event since, the counter is short
  (`CheckEntitlement` reports what the counter holds; there is no fallback read of the aggregator).
- Window counters **are reconciled**, with the checkpoint protocol of the month counters: the aggregator sums the stored rows of the window at the offsets of
  its snapshot (hour rows for an hour, day or week, month rows for a year or the lifetime; see aggregator.md). An outbox duplicate stops counting twice within
  a cycle, and a counter lost with Redis comes back from the aggregator once an event touches it. Short windows are reconciled while they are current
  (an hour, a day or a week that ended more than two hours ago is final and nobody reads it) and only if updated in the last hour; the year and lifetime
  counters are listed in `lim:active:{p}` and reconciled for as long as they are being updated (45 days). A counter that reconciliation had to create is given
  the expiry the apply script would have given it, none for the lifetime counter. Redis lost with its offsets is replayed from the log
  (30-day retention) as before; what the log no longer holds comes from the aggregator.
- A threshold of a monthly limit fires on the month counter only, of a daily limit on the day counters only, and so on. `LimitExceeded` carries `window` and
  `window_id`; `period_id` stays the billing period the window starts in (the current one for the lifetime window), and `event_id` is
  `uuidv5(tenant|customer|meter|window_id|pct)` (for a month that is the period id, as before). The fired marker lives 80 days, a year's until 80 days after the
  year ends, and a lifetime limit's forever: it fires once in the customer's life.
- **Mode** decides `allowed`: `hard` (and a state published before modes) is `used < limit`, `soft` and `notify` are always allowed.
  `LimitStatus` adds `window`, `window_id`, `resets_at` (unset for the lifetime window), `mode`, `over_limit` and `overridden`. A window other than the month
  always reports the current one; `period_id` only picks the month of monthly limits.
- **Enforcement and invoicing agree** because they count the same events: a short window is reconciled against hour totals the aggregator writes in the transaction
  of the month row that invoicing rates, with the same on-time rule; year and lifetime are sums of the very month rows. They can still differ in two bounded
  ways: by the reconciliation interval, and for an event that limits-svc counts as on time by the tenant's current grace while the aggregator classified it by the
  period snapshot (an early-closed or frozen period).
- **UNIQUE and LAST** are counted too ([aggregator.md](aggregator.md#as-built-unique-and-last), [ADR 0025](../adr/0025-unique-and-last-aggregations.md)). The apply script has two more ops next to
  `I` and `M`:
  - `L` (LAST) sets the counter to the event's quantity only if the event is later than the one the counter holds, kept in `lim:ord:{counter}` as `micros:id`. The order is the
    aggregator's (microseconds, then the id bytewise; the script compares ids byte by byte because the `<` of Redis strings follows the server's locale). A redelivery, an event that
    arrives out of order and a replay of the log after Redis lost its offsets all leave the same value.
  - `U` (UNIQUE) adds to the counter the members that are new to `lim:set:{counter}` (the first 16 bytes of the SHA-256 of the value). The counter stays an ordinary number, so
    `CheckEntitlement`, thresholds and the reconciliation correction are unchanged, and an outbox duplicate cannot count twice (the set is idempotent, unlike `INCRBY`). Every counter of the
    entitlement's window has its own set. **The set is bounded** (`PAYRUDDER_UNIQUE_SET_CAP`, default 100 000 members per counter, `limits_unique_set_full`): a full set stops recognising new
    values, so the counter lags until the next reconciliation sets it to the aggregator's exact count. This is the enforcement side only; the exact sets live in the aggregator, and
    nothing here is read by billing. After a Redis loss the counters come back from the aggregator, the sets do not: a value that was forgotten is counted again by its next event (over by
    one) until the following reconciliation takes it out.
  - Reconciliation asks the aggregator for the key with `aggregation` set. A UNIQUE counter is corrected like a sum (`INCRBY -drift`); a LAST counter is set to the aggregator's value **and**
    to the event it names, so an event ordered between the two cannot take the value back afterwards. Both companion keys expire with their counter.
  - Thresholds work on the counter as usual. A LAST value can fall; a threshold marker is per window and percentage, so it fires once per window however the value moves.
