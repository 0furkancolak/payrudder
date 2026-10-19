-- Phase 17: reports group invoices by the payment provider and the customer's country, so both are columns (the snapshot keeps them too).
-- +goose Up
ALTER TABLE invoices
  ADD COLUMN provider text NOT NULL DEFAULT '',
  ADD COLUMN country  char(2);
UPDATE invoices SET provider = COALESCE(snapshot->>'provider', '');
CREATE INDEX invoices_report ON invoices (tenant_id, period_id, currency);

-- The revenue report tells refunded invoices by the saga's history; saga_events is row-level secured like the rest.
GRANT SELECT ON saga_events TO billing_app;

-- +goose Down
DROP INDEX invoices_report;
ALTER TABLE invoices DROP COLUMN country, DROP COLUMN provider;
