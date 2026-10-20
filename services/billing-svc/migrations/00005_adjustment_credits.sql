-- Phase 20: a credit balance has a currency (money in two currencies never mixes), and a credit can come from a plan change as well as from re-rating.
-- +goose Up
ALTER TABLE credit_balances ALTER COLUMN source_rerating_job_id DROP NOT NULL;
ALTER TABLE credit_balances ADD COLUMN source_adjustment_id uuid, ADD COLUMN currency char(3);
ALTER TABLE credit_balances ADD CONSTRAINT credit_has_one_source CHECK ((source_rerating_job_id IS NULL) <> (source_adjustment_id IS NULL));
CREATE UNIQUE INDEX one_credit_per_adjustment ON credit_balances (tenant_id, source_adjustment_id) WHERE source_adjustment_id IS NOT NULL;
-- Credits that exist were made by re-rating, in the currency of the customer's invoices.
UPDATE credit_balances c SET currency = (SELECT i.currency FROM invoices i WHERE i.tenant_id = c.tenant_id AND i.customer_id = c.customer_id ORDER BY i.created_at DESC LIMIT 1);

-- +goose Down
DROP INDEX one_credit_per_adjustment;
ALTER TABLE credit_balances DROP CONSTRAINT credit_has_one_source;
DELETE FROM credit_balances WHERE source_rerating_job_id IS NULL;
ALTER TABLE credit_balances DROP COLUMN currency, DROP COLUMN source_adjustment_id;
ALTER TABLE credit_balances ALTER COLUMN source_rerating_job_id SET NOT NULL;
