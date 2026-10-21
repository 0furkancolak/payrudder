-- Phase 29: the public part of a tenant's bank transfer account (bank name, account holder, IBAN, BIC, reference template, currencies) is printed on its invoices. The
-- account is stored sealed in payments-worker; it copies this part here, and billing-svc freezes it into the invoices it makes. One JSON object; nothing queries inside it.
-- +goose Up
ALTER TABLE tenants ADD COLUMN bank_transfer jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(bank_transfer) = 'object');

-- +goose Down
ALTER TABLE tenants DROP COLUMN bank_transfer;
