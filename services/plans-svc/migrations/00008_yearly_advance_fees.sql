-- Phase 19: yearly subscriptions and fixed fees billed in advance (docs/15-billing-lifecycle.md).
-- A plan version's fixed fee is per interval and currency. The monthly fee in the tenant's currency stays in plan_versions.base_fee_minor;
-- every other combination (a yearly fee in any currency, a monthly fee in another currency) is a plan_version_fees row.

-- +goose Up
ALTER TABLE plan_version_fees ADD COLUMN interval text NOT NULL DEFAULT 'month' CHECK (interval IN ('month', 'year'));
ALTER TABLE plan_version_fees DROP CONSTRAINT plan_version_fees_pkey;
ALTER TABLE plan_version_fees ADD PRIMARY KEY (tenant_id, plan_version_id, currency, interval);

-- fee_timing: 'arrears' bills the fixed fee with the usage at the close of the calendar month (phases 13-18), 'advance' invoices it at the
-- start of each billing period. fee_due_at is the start of the next period to invoice in advance (NULL for arrears subscriptions and for ones a
-- provider bills); billing-svc advances it after the invoice exists.
ALTER TABLE subscriptions
  ADD COLUMN fee_timing text NOT NULL DEFAULT 'arrears' CHECK (fee_timing IN ('advance', 'arrears')),
  ADD COLUMN fee_due_at timestamptz;
CREATE INDEX subscriptions_fee_due ON subscriptions (fee_due_at) WHERE fee_due_at IS NOT NULL;

-- +goose Down
DROP INDEX subscriptions_fee_due;
ALTER TABLE subscriptions DROP COLUMN fee_due_at, DROP COLUMN fee_timing;
DELETE FROM plan_version_fees WHERE interval = 'year';
ALTER TABLE plan_version_fees DROP CONSTRAINT plan_version_fees_pkey;
ALTER TABLE plan_version_fees DROP COLUMN interval;
ALTER TABLE plan_version_fees ADD PRIMARY KEY (tenant_id, plan_version_id, currency);
