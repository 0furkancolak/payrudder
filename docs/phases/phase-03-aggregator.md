# Phase 03 — aggregator

## Goal

Consume `usage.events.v1` and maintain authoritative per-period usage aggregates with exactly-once effect, using
DB-stored offsets plus an event-ID inbox, and deterministic on-time/late classification.

## Scope

**In:** aggregator consumer, `aggregator_db` schema (generation-aware from day one), `pkg/kafkax` DB-offset consumer,
`pkg/period` (`Classify`, cutoff), Kafka DLQ producer, `UsageQueryService` read APIs (`GetCustomerUsage`, `GetUsageBatch`,
`StreamPeriodUsage`, `StreamLateUsage`, `GetIngestWatermark`, `FreezePeriod`), gateway usage route, **100k mini correctness test**.

**Out of scope:** replay jobs and generation promotion (Phase 11; the schema supports them now), billing.

## Tasks

- [x] Migrations: `aggregate_generations` **keyed by tenant** ([ADR 0012](../adr/0012-per-tenant-aggregate-generations.md); no row ⇒ generation 1), `usage_aggregates`, `late_usage`, `processed_events` (LIST partitions), `consumer_offsets`, `period_settings`
- [x] `pkg/period`: `PeriodOf(t, tz)`, `Bounds(period, tz)`, `Cutoff(period, grace)`, `Classify(period, ingestedAt, cutoff)` + exhaustive boundary tests (DST, year end)
- [x] `pkg/kafkax` consumer: `OnPartitionsAssigned` loads offsets from DB and seeks. Per-partition apply loop. Advisory Kafka commit
- [x] Batch apply transaction: dedup insert → pre-summed upserts (SUM/COUNT/MAX merge) → offset upsert with fencing
- [x] `period_settings` snapshot of tenant timezone, period bounds and grace on first event of a period (fetched from plans-svc, cached). Read `FOR SHARE` in the batch transaction
- [x] Per-tenant generation cache in the apply loop (single target in this phase; dual-write path stubbed behind an interface)
- [x] `ClosePeriodEarly` (sandbox tenants only) per [aggregator.md](../03-services/aggregator.md#sandbox-early-close)
- [x] Meter-aware value extraction (`value_property`, `filter`) using cached meter definitions
- [x] Error classification: record error → in-process retries → DLQ with headers. Infra error → pause partition + backoff
- [x] `pkg/kafkax` DLQ producer + header schema
- [x] Read APIs with `aggregator_app` + RLS. `GetUsageBatch` in `REPEATABLE READ` returning per-partition offsets
- [x] `FreezePeriod` + frozen-period violation routing + metric
- [x] Partition maintenance for `processed_events`
- [x] Fault hook `AGG_CRASH_AFTER_COMMIT`
- [x] `tests/chaos` v0: generator (deterministic seed, duplicates, retries) + assertion A1/A2/A3 at 100k events, no infrastructure chaos yet, fault hooks only

## Notes on what was built

- The chaos driver is `tests/chaos` and runs against the compose stack through the public API; `make chaos N=100000` builds ingest-svc and
  aggregator with the `chaos` tag (`deploy/compose/compose.chaos.yaml`) and arms both fault hooks at 0.1. It asserts A1, A2, A3 and A9.
  Container kills and Toxiproxy faults are Phase 09.
- `usage_aggregates` and `late_usage` gained `quantity_scale`, so quantities render as exact decimals.
- The "per-tenant generation cache" is the per-batch `FOR SHARE` read of the tenant's generation rows; an in-memory cache is the optimization to
  measure if that read shows up in profiles. Dual write to a catching-up generation is implemented; replay itself is Phase 11.
- Streams (`StreamPeriodUsage`, `StreamLateUsage`) read the period inside one transaction and send afterwards, so a very large tenant-period is held
  in memory. Fine at this scale; page by customer when that stops being true.
- The 100k chaos run is in `ci.yml` as the `chaos` job.

## Deliverables

- aggregator image. `GET /v1/customers/{id}/usage?period=` works end to end.
- `make chaos N=100000` runs locally and in the PR workflow.

## Acceptance criteria

- [x] Integration: `AGG_CRASH_AFTER_COMMIT=0.1` over 50k events → aggregates exactly equal expected
- [x] Integration: the same event published twice at different offsets → counted once (`aggregator_duplicates_skipped_total` = 1)
- [x] Integration: two consumers on the same partition (forced zombie) → offsets never move backwards, totals exact
- [x] Integration: a malformed record in the middle of a batch → in `usage.events.dlq.v1` with all `x-dlq-*` headers. The other records are applied
- [x] Integration: Postgres stopped for 20 s → no DLQ writes, partition paused, lag recovers to 0 after restart, totals exact
- [x] Unit: `Classify` on-time at `cutoff − 1ms`, late at `cutoff`, across month/year boundaries, for a non-UTC tenant
- [x] Integration: fractional meter (scale 3): events `0.001` × 1000 → aggregate `1.000` exactly
- [x] Integration: `ClosePeriodEarly` concurrent with a running batch → every event classified against exactly one cutoff, live tenant gets `FAILED_PRECONDITION`
- [x] Integration: late event lands in `late_usage` with correct `origin_period_id` and `billed_period_id`
- [x] Chaos v0 (100k events, 5% duplicates, retries, fault hooks): A1–A3 hold exactly

## Dependencies

Phase 02.

## Risks / open questions

- `processed_events` write amplification at volume: measure. Fallback is a bloom-filter pre-check (no correctness change).
- Per-tenant generation lookup is on the hot loop: measure the cache hit path.
- Should meter `filter` be applied at ingest (reject non-matching) or at aggregation (ignore)? Planned: aggregation, so events stay facts.
- Rebalance with franz-go cooperative-sticky + DB offsets: verify the revoke callback waits for in-flight transactions.
