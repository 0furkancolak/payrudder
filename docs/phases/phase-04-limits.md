# Phase 04 — limits-svc

## Goal

Near-real-time entitlement checks backed by Redis counters, `usage.limit-exceeded.v1` events fired exactly once per
threshold, and reconciliation that makes Redis converge to aggregator's totals.

## Scope

**In:** plans-svc outbox relay → `plans.entitlements.v1` (compacted), limits-svc consumers (usage + entitlements),
`apply_batch.lua`, threshold detection, `LimitExceeded` producer, reconciliation with the offset-checkpoint protocol,
`LimitsService` RPCs, gateway entitlement route.

**Out of scope:** notifier email on limit events (Phase 06), webhooks (non-goal).

## Tasks

- [x] `EntitlementState`, `LimitExceeded` protos + golden fixtures
- [x] plans-svc: write outbox rows on subscription create/cancel and plan version publish. Run `pkg/outbox` relay. Tombstones on end
- [x] limits-svc entitlement consumer: per-pod group, read from earliest, in-memory map + Redis mirror. Readiness waits for catch-up
- [x] Counters in scaled units. Overflow guard (2⁵³) + `limits_counter_overflow_total`
- [x] `apply_batch.lua` with offset guard. Loaded via `SCRIPT LOAD`, called with `EVALSHA`
- [x] Per-partition apply loop. Late-event skip using `pkg/period.Classify`
- [x] Threshold crossing in Go from `(before, after, limit)`. `SET NX` fired key. `uuidv5` event ID
- [x] `lim:active:{p}` sorted set maintenance
- [x] Reconciliation: `GetUsageBatch` snapshot `(totals, O_a)` → checkpoint in apply loop → drift correction. Metrics
- [x] `CheckEntitlement` (`MGET`), `GetLimitStatus`, `ReconcileNow`
- [x] Fault hook `LIMITS_CRASH_AFTER_LUA`
- [x] Redis AOF config in compose

## Deliverables

- limits-svc image. `GET /v1/customers/{id}/entitlements/{meter}` returns `used`, `limit`, `allowed`, `as_of`.

## Acceptance criteria

- [x] Integration: a batch redelivered after `LIMITS_CRASH_AFTER_LUA` is skipped (counter unchanged on second apply)
- [x] Integration: a threshold at 80% and 100% is fired exactly once each, across a forced rebalance between two pods
- [x] Integration: counters manually corrupted (+500) → corrected to the aggregator value within one reconciliation cycle; `limits_reconcile_corrections_total` incremented
- [x] Integration: Redis flushed → after reconciliation all active counters equal aggregator totals
- [x] Integration: a late event doesn't increment the current period's counter
- [x] Integration: entitlement change (new subscription) visible in `CheckEntitlement` within 5 s
- [x] Integration: fractional meter limit `"10.5"` → 100% threshold fires on the event that reaches `10.500`
- [x] Unit: reconciliation skips when limits is ahead (`O_l > O_a`), and corrects at the checkpoint otherwise (fake apply loop)

## Dependencies

Phase 03 (aggregator read APIs, `pkg/period`). Phase 01 (plans data).

## Risks / open questions

- The checkpoint protocol adds coupling between the apply loop and reconciliation. Keep it behind a small interface and test it in isolation.
- Redis Cluster hash-tag requirement isn't exercised (single node). Documented only.
- Should `CheckEntitlement` also expose a `soft_limit` / overage price? Out of scope unless cheap.

## Notes (as built)

- Counters follow the aggregator's rules (meter filter, `COUNT` = 1, `MAX`, late = ingested at or after the cutoff) but
  not its early-close/freeze snapshots; a sandbox period closed early shows up as drift and reconciliation adopts the
  aggregator's value.
- **Offsets in Redis.** `lim:off:{p}` is written by the same script as the counters, so a batch is applied exactly once
  (`SKIP` on redelivery, `PARTIAL` re-slices an overlapping batch, `GAP` and `RESET` are errors). When the offset key
  vanishes while the pod had applied records (`RESET`), the consumer rewinds to offset 0 and replays the retained log:
  that is how a flushed Redis recovers without waiting for reconciliation.
- **Thresholds** are evaluated from `(before, after, limit)` after the script; a crash in between (`LIMITS_CRASH_AFTER_LUA`)
  or a failed produce loses nothing: reconciliation ends with a sweep that fires every threshold a counter has reached
  and `lim:fired` does not yet mark. They only fire when the entitlement overlaps the counter's period.
- **Reconciliation is per partition and per tenant** (`GetUsageBatch` is a tenant API), one checkpoint per
  `(tenant, ≤1000 keys)`. The checkpoint is served by the partition's apply loop under the partition lock; an idle
  partition at exactly the checkpoint offset is served immediately. Skips: `ahead`, `no_offset`, `timeout`.
- plans-svc relays `plans.entitlements.v1` through its outbox (`PAYRUDDER_KAFKA_BROKERS`; without it rows wait). Creating a
  subscription publishes one state per entitlement; cancelling publishes the state with `valid_until`, or a tombstone when
  the end is not in the future. Publishing a plan version emits nothing (subscriptions are pinned to a version).
  Known limitation: a subscription that starts in the future replaces the current entitlement of that customer in the
  map until it starts (`valid_from` is honoured, so the customer reads as unlimited in between).
- `GET /v1/customers/{external_id}/entitlements[/{meter}]` in the gateway.
