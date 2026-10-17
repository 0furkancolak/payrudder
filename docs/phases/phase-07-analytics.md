# Phase 07 — analytics-svc

## Goal

Stream usage, limit and invoice facts into ClickHouse and serve fast, tenant-scoped time-series and top-N queries.

## Scope

**In:** ClickHouse schema + migration runner, Go batch consumer for three topics, DLQ routing, `AnalyticsService` RPCs,
gateway analytics route, query builder with mandatory tenant predicate.

**Out of scope:** arbitrary user-defined queries, dashboards for tenants (Phase 12 may show a chart).

## Tasks

- [ ] ClickHouse migrations: `usage_events`, `usage_hourly` + MV, `limit_events`, `invoice_facts`
- [ ] Tiny migration runner (`schema_migrations` table in ClickHouse)
- [ ] Consumer: batch by 10k rows or 1 s per topic, `clickhouse-go/v2` batch insert, commit after insert
- [ ] DLQ for undecodable records (`x-dlq-consumer=analytics`), honouring `x-redrive-target`
- [ ] Query builder: typed filters, `tenant_id` required by construction (no raw SQL path)
- [ ] `QueryUsage` (hour/day granularity, from `usage_hourly`, exact mode from `usage_events FINAL` for ranges ≤ 1 day)
- [ ] `TopCustomers`, `GetIngestStats` (events/s, late %, duplicate % per tenant)
- [ ] Gateway `GET /v1/analytics/usage`

## Deliverables

- analytics-svc image. Usage charts queryable through the public API.

## Acceptance criteria

- [ ] Integration: 10k events with 5% duplicate deliveries → `usage_events FINAL` totals equal aggregator totals exactly
- [ ] Integration: the drift between `usage_hourly` and `FINAL` totals is reported (measured, not asserted zero)
- [ ] Unit: query builder refuses to build a query without `tenant_id` (compile-time via constructor, plus runtime test)
- [ ] Integration: tenant A can't see tenant B's series through the API
- [ ] Integration: ClickHouse stopped for 30 s → no data loss after recovery (lag drains, totals exact)
- [ ] Load (informal): 30-day hourly series for one tenant returns in < 300 ms p95 (**target**, recorded in Phase 9)

## Dependencies

Phase 02 (usage topic). Phases 04/05 for the limit and invoice topics (the consumer can start with usage only).

## Risks / open questions

- Exact vs. rollup semantics might confuse API users. The response includes `"precision": "estimated" | "exact"`.
- Should the rollup dedup via `AggregatingMergeTree` with `uniqExact(event_id)`? Too costly at volume. Revisit if drift is significant.
