-- The UNIQUE and LAST meter aggregations (docs/03-services/aggregator.md, "UNIQUE and LAST").
--
-- LAST keeps, next to the quantity of a row, the event the quantity is the value of: its time (microseconds, what timestamptz holds) and its id. A row is
-- merged by keeping the later (last_at, last_event_id COLLATE "C"), so the result does not depend on the order events arrive in. The columns are NULL for
-- every other aggregation.
--
-- UNIQUE keeps the set of distinct values, because a distinct count is not additive: a year or a day cannot be read from the counts of its months or hours.
-- One row per value (its SHA-256, as a property can be 4 KiB) per customer, meter and billing period, and per wall-clock hour for the quota windows. The
-- count of the billing period stays in usage_aggregates.quantity (and in late_usage for values first seen after the cutoff), kept equal to the number of rows
-- in each state by the transaction that changes them.

-- +goose Up
ALTER TABLE usage_aggregates        ADD COLUMN last_at timestamptz, ADD COLUMN last_event_id text;
ALTER TABLE usage_window_aggregates ADD COLUMN last_at timestamptz, ADD COLUMN last_event_id text;

-- billed_period_id is '' when an on-time event carried the value, else the earliest period a late event carrying it is billed in. '' sorts before every
-- period id, so the state of a value is the minimum of what its events say, which is commutative, associative and idempotent (domain.MergeSeen).
CREATE TABLE usage_distinct_values (
  tenant_id         uuid        NOT NULL,
  customer_id       uuid        NOT NULL,
  meter_code        text        NOT NULL,
  generation        int         NOT NULL,
  period_id         text        NOT NULL,
  value_hash        bytea       NOT NULL CHECK (length(value_hash) = 32),
  billed_period_id  text        NOT NULL DEFAULT '',
  PRIMARY KEY (tenant_id, customer_id, meter_code, generation, period_id, value_hash)
);

-- Only on-time events, like usage_window_aggregates: a rolling window, deleted with the hour totals.
CREATE TABLE usage_distinct_hours (
  tenant_id    uuid   NOT NULL,
  customer_id  uuid   NOT NULL,
  meter_code   text   NOT NULL,
  generation   int    NOT NULL,
  hour_id      text   NOT NULL,
  value_hash   bytea  NOT NULL CHECK (length(value_hash) = 32),
  period_id    text   GENERATED ALWAYS AS (left(hour_id, 7)) STORED,
  PRIMARY KEY (tenant_id, customer_id, meter_code, generation, hour_id, value_hash)
);
CREATE INDEX usage_distinct_hours_hour ON usage_distinct_hours (hour_id);

ALTER TABLE usage_distinct_values ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_distinct_values FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON usage_distinct_values USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
ALTER TABLE usage_distinct_hours ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_distinct_hours FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON usage_distinct_hours USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

GRANT SELECT ON usage_distinct_values, usage_distinct_hours TO aggregator_app;
-- The consumer writes them, maintenance and a discarded or reset generation delete them.
GRANT SELECT, INSERT, UPDATE, DELETE ON usage_distinct_values, usage_distinct_hours TO aggregator_system;

-- +goose Down
DROP TABLE usage_distinct_hours, usage_distinct_values;
ALTER TABLE usage_window_aggregates DROP COLUMN last_event_id, DROP COLUMN last_at;
ALTER TABLE usage_aggregates        DROP COLUMN last_event_id, DROP COLUMN last_at;
