-- Phase 22: invoice lines can be discounts and tax, and say which service period they cover.
-- +goose Up
ALTER TABLE invoice_lines DROP CONSTRAINT invoice_lines_kind_check;
ALTER TABLE invoice_lines ADD CONSTRAINT invoice_lines_kind_check CHECK (kind IN ('BASE_FEE','USAGE','LATE_USAGE','CREDIT','CORRECTION','DISCOUNT','TAX'));
ALTER TABLE invoice_lines ADD COLUMN service_from timestamptz, ADD COLUMN service_to timestamptz;

-- +goose Down
ALTER TABLE invoice_lines DROP COLUMN service_to, DROP COLUMN service_from;
DELETE FROM invoice_lines WHERE kind IN ('DISCOUNT','TAX');
ALTER TABLE invoice_lines DROP CONSTRAINT invoice_lines_kind_check;
ALTER TABLE invoice_lines ADD CONSTRAINT invoice_lines_kind_check CHECK (kind IN ('BASE_FEE','USAGE','LATE_USAGE','CREDIT','CORRECTION'));
