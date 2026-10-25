-- Phase 44b: the ledger's "the same fact twice is one row" index must not span tenants. The bank-transfer reference defaults to the invoice number, and two
-- tenants can both have INV-2026-000001; without tenant_id the second tenant's pending row was dropped silently by ON CONFLICT DO NOTHING.
-- +goose Up
DROP INDEX ledger_fact_once;
CREATE UNIQUE INDEX ledger_fact_once ON payment_ledger (tenant_id, provider, kind, provider_ref, status) WHERE provider_ref IS NOT NULL;

-- +goose Down
DROP INDEX ledger_fact_once;
CREATE UNIQUE INDEX ledger_fact_once ON payment_ledger (provider, kind, provider_ref, status) WHERE provider_ref IS NOT NULL;
