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

1. Collect active counter keys from a sorted set `lim:active:{p}` (score = last update).
2. Call aggregator `GetUsageBatch(keys, partition=p)`. Aggregator reads the totals **and** its
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
package quotify.limits.v1;

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

Quotify **does not block ingest** when a limit is exceeded. Usage events are facts that already happened.
Tenants call `CheckEntitlement` before serving their customer and decide what to do.

## Failure modes

| Failure | Behaviour |
|---|---|
| Redis restart without persistence | Counters reset → reconciliation restores them within one cycle. `CheckEntitlement` returns low numbers until then (documented risk). AOF `everysec` enabled to shrink this window |
| limits-svc lag | `as_of` shows staleness. Alert at lag > 10 s |
| Duplicate event (outbox republish) | Counter over-counts by that event until next reconciliation |
| Threshold crossed by two pods simultaneously | Impossible per key: a partition has one owner, and `SET NX` covers handover races |

## Scaling notes

One Lua call per batch per partition. Parallelism = partitions. Redis single-node ceiling is far above
our ingest **targets**. `CheckEntitlement` is a single `MGET` (counter + entitlement).
