-- Quotas: a meter can be filed under a category of the tenant's own, an entitlement counts over a window (the billing month as before, or an hour, a day,
-- an ISO week) with an enforcement mode, and one customer can have its own limit on a meter instead of its plan version's.
--
-- Defaults keep every existing row what it was: no category, the month, hard. The included quantity of a price lives in plan_prices.config with the rest
-- of the price (validated in Go), so it needs no column.

-- +goose Up
ALTER TABLE meters ADD COLUMN category text NOT NULL DEFAULT '' CHECK (length(category) <= 64);

ALTER TABLE plan_entitlements
  ADD COLUMN quota_window text NOT NULL DEFAULT 'month' CHECK (quota_window IN ('hour', 'day', 'week', 'month')),
  ADD COLUMN mode         text NOT NULL DEFAULT 'hard'  CHECK (mode IN ('hard', 'soft', 'notify'));

CREATE TABLE customer_entitlement_overrides (
  tenant_id             uuid NOT NULL,
  customer_id           uuid NOT NULL,
  meter_code            text NOT NULL,
  limit_units           bigint NOT NULL CHECK (limit_units >= 0),   -- scaled units of the meter; 0 = unlimited
  alert_thresholds_pct  int[]  NOT NULL DEFAULT '{80,100}',
  quota_window          text NOT NULL DEFAULT 'month' CHECK (quota_window IN ('hour', 'day', 'week', 'month')),
  mode                  text NOT NULL DEFAULT 'hard'  CHECK (mode IN ('hard', 'soft', 'notify')),
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, customer_id, meter_code),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id)
);

ALTER TABLE customer_entitlement_overrides ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_entitlement_overrides FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_entitlement_overrides USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON customer_entitlement_overrides TO plans_app;
-- The sandbox reset deletes them with the customers they belong to.
GRANT SELECT, DELETE ON customer_entitlement_overrides TO plans_system;

-- +goose Down
DROP TABLE customer_entitlement_overrides;
ALTER TABLE plan_entitlements DROP COLUMN mode, DROP COLUMN quota_window;
ALTER TABLE meters DROP COLUMN category;
