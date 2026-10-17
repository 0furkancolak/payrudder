# ADR 0012 — Per-tenant aggregate generations

- Status: Accepted
- Date: 2026-10-17

## Context

Replay rebuilds usage aggregates from the Kafka log into a new **generation** next to the current one, verifies a diff, then
promotes it. Replays are almost always triggered for one tenant (its meter filter was wrong, its backfill was broken). With a single
global active generation, a one-tenant replay must still copy or re-aggregate every other tenant before the flip, and promotion
pauses live consumption for everyone.

## Decision

- Generations are keyed by `(tenant_id, generation)`. Each tenant has at most one `active` and at most one open
  (`building` / `catching_up`) generation. A tenant without a row uses generation 1.
- A replay job targets exactly one tenant. "All tenants" is a batch of independent jobs.
- Promotion does **not** pause live traffic. Once the replay reaches its end-offset snapshot, the live consumer dual-writes that
  tenant's events into both generations. The replay consumer stops at the first dual-written offset. Overlap is absorbed by
  `processed_events`, which is keyed by generation. The flip is one transaction on the tenant's generation rows. The live
  consumer reads those rows `FOR SHARE` in each batch, so no batch straddles a flip.
- Billing runs and re-rating jobs pin `(tenant, generation)`.

## Alternatives considered

| Option | Why not |
|---|---|
| Global active generation | A one-tenant fix costs a full re-aggregation (or copy) of all tenants, and the pause affects everyone |
| Global generation + copy untouched tenants | Copy volume scales with total data, and it's easy to get the copy/catch-up boundary wrong |
| Pause-and-catch-up promotion (per tenant) | Simpler, but pausing a partition stalls every tenant on it. Dual-write keeps the pause at zero |

## Consequences

- The live consumer needs a per-tenant generation cache plus a dual-write path. It's more code in the hottest loop, so it gets a
  dedicated integration test (promotion under load with a ledger check).
- Reads resolve `active_generation(tenant_id)`, a tiny indexed lookup, cached per request.
- Schema impact is fixed in Phase 03 (`aggregate_generations` keyed by tenant) even though replay ships in Phase 11.
