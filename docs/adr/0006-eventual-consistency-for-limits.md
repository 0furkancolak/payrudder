# ADR 0006 — Eventually consistent limit enforcement

- Status: Accepted
- Date: 2026-10-17

## Context

Tenants want to know, within seconds, whether a customer exceeded an entitlement (e.g. 1M tokens/month). Usage arrives through
Kafka and is processed by many pods. A strongly consistent check would need a synchronous counter update on the ingest path.

## Decision

- limits-svc maintains **Redis counters** fed asynchronously from `usage.events.v1`. Each partition's batch is applied by one Lua
  script that also stores the partition offset (redelivery-safe).
- `CheckEntitlement` is **eventually consistent** and returns `as_of` so callers see staleness. **Target** p99 staleness < 2 s.
- Threshold crossings emit `usage.limit-exceeded.v1` once per (customer, meter, period, threshold), with a deterministic event ID.
- **Reconciliation** against aggregator's authoritative totals, at the same offset prefix per partition, corrects drift every 5 min. It covers every
  quota window, not only the billing month: the aggregator keeps month totals (read for a month, a year and the lifetime) and hour totals (read for an
  hour, a day and an ISO week), see [aggregator.md](../03-services/aggregator.md#as-built-window-totals-quota-windows).
- The counters of `UNIQUE` (a bounded set of value digests) and `LAST` (the value with its event) meters are reconciled the same way, to the aggregator's exact count and event ([ADR 0025](0025-unique-and-last-aggregations.md)).
- Payrudder **doesn't reject usage events** over the limit. Enforcement is the tenant's decision.
- Billing never reads Redis.

## Alternatives considered

| Option | Why not |
|---|---|
| Synchronous `CheckAndIncrement` in ingest | Linearizable, but couples ingest availability and latency to Redis and adds a hop for every event. A Redis outage would become an ingest outage |
| Count in Postgres (aggregator) and query it | Correct, but heavier reads and higher latency. Aggregator is tuned for batch writes |
| Per-pod in-memory counters | Wrong with more than one pod. Lost on restart |
| Distributed lock per customer | Throughput collapse under hot customers |

## Consequences

- Customers can overshoot a limit by roughly *rate × staleness*. That's documented in the API reference, and tenants needing hard caps
  should apply a safety margin.
- Redis data loss is recoverable (reconciliation), so Redis is a cache of derived state, not a source of truth.
- A future "prepaid hard stop" would need a different, synchronous mechanism and its own ADR.
