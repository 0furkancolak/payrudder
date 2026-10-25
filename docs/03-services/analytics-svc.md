# analytics-svc

## Responsibility

Store raw usage events and billing facts in ClickHouse and serve time-series and top-N queries for
dashboards (tenant-facing usage charts, operator ingest stats).

**Analytics numbers aren't billing numbers.** They're near-real-time and *eventually* deduplicated.
Invoices always come from aggregator + billing-svc. The UI labels analytics totals "estimated".

## Why a Go consumer instead of the ClickHouse Kafka engine

- We control batching (10k rows or 1 s), protobuf decoding, DLQ routing and trace spans.
- The Kafka engine with protobuf needs schema files deployed into ClickHouse and makes failures opaque.
- Offsets: committed to Kafka **after** a successful batch insert (at-least-once). Duplicates are tolerated
  because the table engine dedups (below). This is a deliberate contrast to aggregator's inbox pattern:
  here the store absorbs duplicates cheaply, so the extra complexity isn't needed.

## ClickHouse schema (sketch)

```sql
CREATE TABLE usage_events
(
    tenant_id      UUID,
    event_id       String,
    customer_id    UUID,
    meter_code     LowCardinality(String),
    quantity_units Int64,                    -- scaled units (ADR 0010)
    quantity_scale UInt8,
    period_id      LowCardinality(String),   -- tenant time zone, from the event
    event_time     DateTime64(3, 'UTC'),
    ingested_at    DateTime64(3, 'UTC'),
    properties     Map(LowCardinality(String), String)
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY toYYYYMM(event_time)
ORDER BY (tenant_id, meter_code, customer_id, event_time, event_id)
TTL toDateTime(event_time) + INTERVAL 13 MONTH;

-- hourly rollup; fed from usage_events, dedup approximated (see note)
CREATE TABLE usage_hourly
(
    tenant_id UUID, meter_code LowCardinality(String), customer_id UUID,
    hour DateTime('UTC'), quantity_units_sum Int64, quantity_scale UInt8, event_count UInt64
)
ENGINE = SummingMergeTree((quantity_units_sum, event_count))   -- quantity_scale is constant per meter, never summed
ORDER BY (tenant_id, meter_code, customer_id, hour);

CREATE MATERIALIZED VIEW usage_hourly_mv TO usage_hourly AS
SELECT tenant_id, meter_code, customer_id, toStartOfHour(event_time) AS hour,
       sum(quantity_units) AS quantity_units_sum, any(quantity_scale) AS quantity_scale, count() AS event_count
FROM usage_events GROUP BY tenant_id, meter_code, customer_id, hour;

CREATE TABLE limit_events (...) ENGINE = ReplacingMergeTree ORDER BY (tenant_id, event_id);
CREATE TABLE invoice_facts (...) ENGINE = ReplacingMergeTree(version) ORDER BY (tenant_id, invoice_id);
```

Note: the materialized view sees duplicates before `ReplacingMergeTree` merges them, so `usage_hourly`
can over-count by the (small) duplicate rate. Queries that need exactness over short ranges hit
`usage_events FINAL`. Dashboards use `usage_hourly`. The chaos test measures this drift (Phase 9).

**Tenant isolation:** ClickHouse has row policies, but the service is the only client. Every query is built
by a query builder that **requires** `tenant_id` as the first `WHERE` predicate (it's also the first
`ORDER BY` column, so it doubles as the primary index). There's no raw SQL from callers.

## gRPC API (sketch)

```proto
package payrudder.analytics.v1;

service AnalyticsService {
  rpc QueryUsage(QueryUsageRequest) returns (QueryUsageResponse);          // meter, customer?, from, to, granularity HOUR|DAY
  rpc TopCustomers(TopCustomersRequest) returns (TopCustomersResponse);    // meter, period, limit ≤ 100
  rpc GetIngestStats(GetIngestStatsRequest) returns (GetIngestStatsResponse); // operator: events/s per tenant, late %, dup %
}
```

## Consumed messages

| Topic | Group | Table |
|---|---|---|
| `usage.events.v1` | `analytics` | `usage_events` |
| `usage.limit-exceeded.v1` | `analytics` | `limit_events` |
| `billing.invoices.v1` | `analytics` | `invoice_facts` |

## Produced messages

`usage.events.dlq.v1` for undecodable records (same DLQ as aggregator, with header `x-dlq-consumer=analytics`).

## Failure modes

| Failure | Behaviour |
|---|---|
| ClickHouse down | Consumer stops committing, lag grows, no data loss within Kafka retention |
| Duplicate inserts | Merged by `ReplacingMergeTree`. Rollup drift bounded and measured |
| Crash after insert, before commit | Batch re-inserted → duplicates → as above |

## Scaling notes

Insert big batches (≥ 10k rows) to avoid too many parts. One consumer per partition is plenty.
**Target:** query p95 < 300 ms for a 30-day hourly series of one tenant.
