-- Hour totals, so that limits-svc can reconcile its hour, day and ISO-week quota counters the way it reconciles the monthly ones (docs/03-services/aggregator.md,
-- "Window totals"). One row per customer, meter and wall-clock hour of the tenant ("2026-11-05T13"), written in the same transaction and under the same inbox as the month
-- row of usage_aggregates, for on-time events only. A day or a week is the sum (or, for a MAX meter, the maximum) of its hour rows; a year or the lifetime is read from the
-- month rows, which are kept forever.
--
-- The rows are a rolling window, not a ledger: billing never reads them, maintenance deletes the hours older than two weeks, and an event whose hour is older than that is not
-- written at all. period_id is the month the hour belongs to, so a replay carries the hours of the periods it did not rebuild over to the new generation like the month rows.

-- +goose Up
CREATE TABLE usage_window_aggregates (
  tenant_id       uuid        NOT NULL,
  customer_id     uuid        NOT NULL,
  meter_code      text        NOT NULL,
  generation      int         NOT NULL,
  hour_id         text        NOT NULL,
  period_id       text        GENERATED ALWAYS AS (left(hour_id, 7)) STORED,
  quantity        numeric(38, 0) NOT NULL,
  quantity_scale  smallint    NOT NULL CHECK (quantity_scale BETWEEN 0 AND 6),
  event_count     bigint      NOT NULL,
  updated_at      timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, customer_id, meter_code, generation, hour_id)
);
CREATE INDEX usage_window_aggregates_hour ON usage_window_aggregates (hour_id);

ALTER TABLE usage_window_aggregates ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_window_aggregates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON usage_window_aggregates USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

GRANT SELECT ON usage_window_aggregates TO aggregator_app;
-- The consumer writes them, maintenance and a discarded or reset generation delete them.
GRANT SELECT, INSERT, UPDATE, DELETE ON usage_window_aggregates TO aggregator_system;

-- +goose Down
DROP TABLE usage_window_aggregates;
