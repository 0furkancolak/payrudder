-- Phase 19: a fixed fee invoiced in advance is its own invoice (kind FEE), one per subscription and fee period, found again by fee_key.
-- +goose Up
ALTER TABLE invoices DROP CONSTRAINT invoices_kind_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_kind_check CHECK (kind IN ('REGULAR','CORRECTION','FEE'));
ALTER TABLE invoices ADD COLUMN fee_key text;   -- '{subscription id}|{period start, RFC 3339 UTC}' for kind FEE
CREATE UNIQUE INDEX one_fee_invoice ON invoices (tenant_id, fee_key) WHERE fee_key IS NOT NULL;

-- +goose Down
DROP INDEX one_fee_invoice;
ALTER TABLE invoices DROP COLUMN fee_key;
DELETE FROM invoices WHERE kind = 'FEE';
ALTER TABLE invoices DROP CONSTRAINT invoices_kind_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_kind_check CHECK (kind IN ('REGULAR','CORRECTION'));
