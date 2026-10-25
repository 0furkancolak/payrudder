-- A credit from re-rating has the currency of the invoices it corrects. Until now it was written without one, and a credit without a currency is applied to an invoice in any
-- currency (ListUnappliedCredits takes "this currency or none"): a customer who later moved to another currency had it taken off there as if the amounts were alike.
-- Re-rating prices in the tenant's currency and skips subscriptions sold in another one, so the credit is in the currency of the customer's regular invoice of the period.

-- +goose Up
-- The migrator owns the tables and RLS is forced on them, so it sees no rows without a tenant: the owner is exempt while FORCE is off, and it is on again before the
-- migration ends (see 00032).
ALTER TABLE credit_balances NO FORCE ROW LEVEL SECURITY;
ALTER TABLE invoices NO FORCE ROW LEVEL SECURITY;
ALTER TABLE rerating_jobs NO FORCE ROW LEVEL SECURITY;
UPDATE credit_balances c SET currency = (
  SELECT i.currency FROM rerating_jobs j
  JOIN invoices i ON i.tenant_id = j.tenant_id AND i.period_id = j.period_id AND i.customer_id = c.customer_id AND i.kind = 'REGULAR'
  WHERE j.tenant_id = c.tenant_id AND j.id = c.source_rerating_job_id
  LIMIT 1)
WHERE c.currency IS NULL AND c.source_rerating_job_id IS NOT NULL;
ALTER TABLE credit_balances FORCE ROW LEVEL SECURITY;
ALTER TABLE invoices FORCE ROW LEVEL SECURITY;
ALTER TABLE rerating_jobs FORCE ROW LEVEL SECURITY;

-- +goose Down
-- The currencies stay: the data was wrong without them.
SELECT 1;
