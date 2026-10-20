-- Phase 23: per-tenant dunning. A tenant may set the delays between a declined charge and each retry; without a row the
-- service-wide defaults apply (+1 d, +3 d, +7 d; minutes in sandbox).
-- +goose Up
CREATE TABLE dunning_settings (
  tenant_id     uuid PRIMARY KEY,
  retry_hours   int[] NOT NULL CHECK (cardinality(retry_hours) <= 6),  -- delay before retry 1..n, in hours (minutes in a sandbox tenant)
  updated_at    timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE dunning_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE dunning_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON dunning_settings USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, UPDATE ON dunning_settings TO billing_app;
GRANT SELECT ON dunning_settings TO billing_system;

-- +goose Down
DROP TABLE dunning_settings;
