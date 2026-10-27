-- Money received by hand (invoice_payments) for an invoice that is then voided before it was paid in full has no card to go back to. It becomes a credit balance of the
-- customer, one per voided invoice: a fourth source next to a re-rating job, a plan change's adjustment and a credit note.

-- +goose Up
ALTER TABLE credit_balances ADD COLUMN source_invoice_id uuid;
ALTER TABLE credit_balances DROP CONSTRAINT credit_has_one_source;
ALTER TABLE credit_balances ADD CONSTRAINT credit_has_one_source CHECK (num_nonnulls(source_rerating_job_id, source_adjustment_id, source_credit_note_id, source_invoice_id) = 1);
CREATE UNIQUE INDEX one_credit_per_voided_invoice ON credit_balances (tenant_id, source_invoice_id) WHERE source_invoice_id IS NOT NULL;

-- +goose Down
DROP INDEX one_credit_per_voided_invoice;
ALTER TABLE credit_balances DROP CONSTRAINT credit_has_one_source;
DELETE FROM credit_balances WHERE source_invoice_id IS NOT NULL;
ALTER TABLE credit_balances DROP COLUMN source_invoice_id;
ALTER TABLE credit_balances ADD CONSTRAINT credit_has_one_source CHECK (num_nonnulls(source_rerating_job_id, source_adjustment_id, source_credit_note_id) = 1);
