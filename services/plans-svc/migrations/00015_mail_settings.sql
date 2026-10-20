-- Phase 33: a tenant can switch Quotify's outgoing e-mail off per category (invoice issued, payment receipts, payment failed, usage alerts, system).
-- No row means every category is on; the row holds the categories that are off. Notifier reads it before it sends (a disabled category is dropped and counted).

-- +goose Up
CREATE TABLE tenant_mail_settings (
  tenant_id   uuid PRIMARY KEY,
  disabled    text[] NOT NULL DEFAULT '{}'
              CHECK (disabled <@ ARRAY['invoice_issued', 'payment_receipts', 'payment_failed', 'usage_alerts', 'system']::text[]),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE tenant_mail_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_mail_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_mail_settings USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, UPDATE ON tenant_mail_settings TO plans_app;
GRANT SELECT ON tenant_mail_settings TO plans_system;

-- +goose Down
DROP TABLE tenant_mail_settings;
