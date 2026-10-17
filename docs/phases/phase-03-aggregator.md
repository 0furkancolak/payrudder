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

- [ ] Migrations: `aggregate_generations` (seed generation 1 active), `usage_aggregates`, `late_usage`, `processed_events` (LIST partitions), `consumer_offsets`, `period_settings`
- [ ] `pkg/period`: `PeriodOf(t)`, `Cutoff(period, grace)`, `Classify(eventTime, ingestedAt, grace)` + exhaustive boundary tests
- [ ] `pkg/kafkax` consumer: `OnPartitionsAssigned` loads offsets from DB and seeks. Per-partition apply loop. Advisory Kafka commit
- [ ] Batch apply transaction: dedup insert → pre-summed upserts (SUM/COUNT/MAX merge) → offset upsert with fencing
- [ ] `period_settings` snapshot of tenant grace on first event of a period (grace fetched from plans-svc, cached)
- [ ] Meter-aware value extraction (`value_property`, `filter`) using cached meter definitions
- [ ] Error classification: record error → in-process retries → DLQ with headers. Infra error → pause partition + backoff
- [ ] `pkg/kafkax` DLQ producer + header schema
- [ ] Read APIs with `aggregator_app` + RLS. `GetUsageBatch` in `REPEATABLE READ` returning per-partition offsets
- [ ] `FreezePeriod` + frozen-period violation routing + metric
- [ ] Partition maintenance for `processed_events`
- [ ] Fault hook `AGG_CRASH_AFTER_COMMIT`
- [ ] `tests/chaos` v0: generator (deterministic seed, duplicates, retries) + assertion A1/A2/A3 at 100k events, no infrastructure chaos yet, fault hooks only

## Deliverables

- aggregator image. `GET /v1/customers/{id}/usage?period=` works end to end.
- `make chaos N=100000` runs locally and in the PR workflow.

## Acceptance criteria

- [ ] Integration: `AGG_CRASH_AFTER_COMMIT=0.1` over 50k events → aggregates exactly equal expected
- [ ] Integration: the same event published twice at different offsets → counted once (`aggregator_duplicates_skipped_total` = 1)
- [ ] Integration: two consumers on the same partition (forced zombie) → offsets never move backwards, totals exact
- [ ] Integration: a malformed record in the middle of a batch → in `usage.events.dlq.v1` with all `x-dlq-*` headers. The other records are applied
- [ ] Integration: Postgres stopped for 20 s → no DLQ writes, partition paused, lag recovers to 0 after restart, totals exact
- [ ] Unit: `Classify` on-time at `cutoff − 1ms`, late at `cutoff`, across month/year boundaries
- [ ] Integration: late event lands in `late_usage` with correct `origin_period_id` and `billed_period_id`
- [ ] Chaos v0 (100k events, 5% duplicates, retries, fault hooks): A1–A3 hold exactly

## Dependencies

Phase 02.

## Risks / open questions

- `processed_events` write amplification at volume: measure. Fallback is a bloom-filter pre-check (no correctness change).
- Should meter `filter` be applied at ingest (reject non-matching) or at aggregation (ignore)? Planned: aggregation, so events stay facts.
- Rebalance with franz-go cooperative-sticky + DB offsets: verify the revoke callback waits for in-flight transactions.
