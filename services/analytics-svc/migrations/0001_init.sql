-- ClickHouse schema of analytics-svc (docs/03-services/analytics-svc.md). Statements are separated by a semicolon at the
-- end of a line; the migration runner applies each file once and records it in schema_migrations.

CREATE TABLE IF NOT EXISTS usage_events
(
    tenant_id             UUID,
    event_id              String,
    customer_id           UUID,
    customer_external_id  String,
    meter_code            LowCardinality(String),
    quantity_units        Int64,
    quantity_scale        UInt8,
    period_id             LowCardinality(String),
    event_time            DateTime64(3, 'UTC'),
    ingested_at           DateTime64(3, 'UTC'),
    properties            Map(LowCardinality(String), String)
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY toYYYYMM(event_time)
ORDER BY (tenant_id, meter_code, customer_id, event_time, event_id)
TTL toDateTime(event_time) + INTERVAL 13 MONTH;

CREATE TABLE IF NOT EXISTS usage_hourly
(
    tenant_id           UUID,
    meter_code          LowCardinality(String),
    customer_id         UUID,
    hour                DateTime('UTC'),
    quantity_units_sum  Int64,
    quantity_scale      UInt8,
    event_count         UInt64
)
ENGINE = SummingMergeTree((quantity_units_sum, event_count))
ORDER BY (tenant_id, meter_code, customer_id, hour);

CREATE MATERIALIZED VIEW IF NOT EXISTS usage_hourly_mv TO usage_hourly AS
SELECT tenant_id, meter_code, customer_id, toStartOfHour(event_time) AS hour,
       sum(quantity_units) AS quantity_units_sum, any(quantity_scale) AS quantity_scale, count() AS event_count
FROM usage_events
GROUP BY tenant_id, meter_code, customer_id, hour;

CREATE TABLE IF NOT EXISTS limit_events
(
    tenant_id       UUID,
    event_id        String,
    customer_id     UUID,
    meter_code      LowCardinality(String),
    period_id       LowCardinality(String),
    threshold_pct   Int32,
    limit_units     Int64,
    used_units      Int64,
    quantity_scale  UInt8,
    detected_at     DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
ORDER BY (tenant_id, event_id);

CREATE TABLE IF NOT EXISTS invoice_facts
(
    tenant_id     UUID,
    invoice_id    UUID,
    version       Int64,
    customer_id   UUID,
    period_id     LowCardinality(String),
    kind          LowCardinality(String),
    total_minor   Int64,
    currency      LowCardinality(String),
    number        String,
    occurred_at   DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
ORDER BY (tenant_id, invoice_id, version);
