# ADR 0008 — ClickHouse for analytics

- Status: Accepted
- Date: 2026-10-17

## Context

Tenants want usage charts (hourly/daily series per meter or customer, top customers) over months of raw events. Operators
want ingest stats per tenant. These are scan-heavy aggregations over hundreds of millions of rows, a poor fit for the
OLTP Postgres databases, which are tuned for transactional upserts.

## Decision

- **ClickHouse** stores raw events (`usage_events`, `ReplacingMergeTree` on `event_id`) plus an hourly rollup
  (`SummingMergeTree` via materialized view), limit events and invoice facts.
- analytics-svc consumes Kafka with a **Go consumer** (batch inserts ≥ 10k rows or 1 s) rather than ClickHouse's Kafka engine.
- Analytics is explicitly **not billing-grade**: at-least-once inserts, eventual dedup. Invoices always come from aggregator/billing.
- Tenant isolation through a query builder that requires `tenant_id` (leading `ORDER BY` key).
- Go driver: `github.com/ClickHouse/clickhouse-go/v2` (native protocol, batch API).

## Alternatives considered

| Option | Why not |
|---|---|
| Postgres + TimescaleDB | Good for moderate volumes and one less technology, but weaker compression and scan speed at our target volumes, and it would compete with OLTP |
| Querying aggregator's Postgres | Only has period totals. No time series, and it would load the billing-critical DB |
| Apache Druid / Pinot | Built for real-time OLAP, but operationally heavy (many node types) for one developer |
| ClickHouse Kafka engine | Less control over protobuf decoding, DLQ and tracing. Failures are harder to see |

## Consequences

- Fast analytical queries with high compression. The ClickHouse skill is demonstrated.
- Another stateful component to run (single node locally).
- Duplicates can briefly inflate rollups. The drift is measured in the chaos suite and labelled "estimated" in the API.
- Schema migrations for ClickHouse are separate SQL files in `services/analytics-svc/migrations`, applied by a small runner (no goose dialect).
