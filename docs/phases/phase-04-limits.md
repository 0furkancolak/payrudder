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

- [ ] `EntitlementState`, `LimitExceeded` protos + golden fixtures
- [ ] plans-svc: write outbox rows on subscription create/cancel and plan version publish. Run `pkg/outbox` relay. Tombstones on end
- [ ] limits-svc entitlement consumer: per-pod group, read from earliest, in-memory map + Redis mirror. Readiness waits for catch-up
- [ ] `apply_batch.lua` with offset guard. Loaded via `SCRIPT LOAD`, called with `EVALSHA`
- [ ] Per-partition apply loop. Late-event skip using `pkg/period.Classify`
- [ ] Threshold crossing in Go from `(before, after, limit)`. `SET NX` fired key. `uuidv5` event ID
- [ ] `lim:active:{p}` sorted set maintenance
- [ ] Reconciliation: `GetUsageBatch` snapshot `(totals, O_a)` → checkpoint in apply loop → drift correction. Metrics
- [ ] `CheckEntitlement` (`MGET`), `GetLimitStatus`, `ReconcileNow`
- [ ] Fault hook `LIMITS_CRASH_AFTER_LUA`
- [ ] Redis AOF config in compose

## Deliverables

- limits-svc image. `GET /v1/customers/{id}/entitlements/{meter}` returns `used`, `limit`, `allowed`, `as_of`.

## Acceptance criteria

- [ ] Integration: a batch redelivered after `LIMITS_CRASH_AFTER_LUA` is skipped (counter unchanged on second apply)
- [ ] Integration: a threshold at 80% and 100% is fired exactly once each, across a forced rebalance between two pods
- [ ] Integration: counters manually corrupted (+500) → corrected to the aggregator value within one reconciliation cycle; `limits_reconcile_corrections_total` incremented
- [ ] Integration: Redis flushed → after reconciliation all active counters equal aggregator totals
- [ ] Integration: a late event doesn't increment the current period's counter
- [ ] Integration: entitlement change (new subscription) visible in `CheckEntitlement` within 5 s
- [ ] Unit: reconciliation skips when limits is ahead (`O_l > O_a`), and corrects at the checkpoint otherwise (fake apply loop)

## Dependencies

Phase 03 (aggregator read APIs, `pkg/period`). Phase 01 (plans data).

## Risks / open questions

- The checkpoint protocol adds coupling between the apply loop and reconciliation. Keep it behind a small interface and test it in isolation.
- Redis Cluster hash-tag requirement isn't exercised (single node). Documented only.
- Should `CheckEntitlement` also expose a `soft_limit` / overage price? Out of scope unless cheap.
