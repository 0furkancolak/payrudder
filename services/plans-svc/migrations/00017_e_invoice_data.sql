-- Phase 30: the data an e-invoice needs. The seller's company data and when an invoice becomes an e-invoice live on the tenant; the buyer's tax id,
-- tax office and e-fatura registration flag live on the customer. Nothing here is checked against the tax authority.

-- +goose Up
ALTER TABLE tenants
  ADD COLUMN legal jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(legal) = 'object'),
  ADD COLUMN e_invoice_on text NOT NULL DEFAULT 'off' CHECK (e_invoice_on IN ('off', 'finalized', 'paid'));

ALTER TABLE customers
  ADD COLUMN tax_id text NOT NULL DEFAULT '',
  ADD COLUMN tax_office text NOT NULL DEFAULT '',
  ADD COLUMN e_invoice_registered boolean NOT NULL DEFAULT false;
GRANT UPDATE (tax_id, tax_office, e_invoice_registered) ON customers TO plans_app;

-- +goose Down
ALTER TABLE customers DROP COLUMN e_invoice_registered, DROP COLUMN tax_office, DROP COLUMN tax_id;
ALTER TABLE tenants DROP COLUMN e_invoice_on, DROP COLUMN legal;
