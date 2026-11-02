-- Two money rules (docs/code-quality/billing-svc-status.md, defaults not agreed), both additive: nothing already written is changed.
--
-- 1. A credit note gives back the tax of what it credits. credit_notes.tax_minor is the part of total_minor that is tax: the note's share of the invoice's total taken
--    from the invoice's tax (creditnotes/model.TaxShare), so the notes of one invoice add up to its tax. A note written before this column has 0.
-- 2. A negative invoice total is no longer lost. The invoice's total is still clamped to zero (invoices.total_minor >= 0), and the surplus becomes a credit balance of its
--    own source: credit_balances.source_surplus_invoice_id, at most one per invoice (idempotent), applied to the customer's next invoices like every other credit.
--    source_invoice_id is not reused: it means "money received for an invoice that was voided" and the accounting refunds read it as that.
-- +goose Up
ALTER TABLE credit_notes ADD COLUMN tax_minor bigint NOT NULL DEFAULT 0 CONSTRAINT credit_note_tax_within_total CHECK (tax_minor >= 0 AND tax_minor <= total_minor);

ALTER TABLE credit_balances ADD COLUMN source_surplus_invoice_id uuid;
ALTER TABLE credit_balances DROP CONSTRAINT credit_has_one_source;
ALTER TABLE credit_balances ADD CONSTRAINT credit_has_one_source
  CHECK (num_nonnulls(source_rerating_job_id, source_adjustment_id, source_credit_note_id, source_invoice_id, source_surplus_invoice_id) = 1);
CREATE UNIQUE INDEX one_credit_per_surplus_invoice ON credit_balances (tenant_id, source_surplus_invoice_id) WHERE source_surplus_invoice_id IS NOT NULL;

-- +goose Down
DROP INDEX one_credit_per_surplus_invoice;
ALTER TABLE credit_balances DROP CONSTRAINT credit_has_one_source;
ALTER TABLE credit_balances DROP COLUMN source_surplus_invoice_id;
ALTER TABLE credit_balances ADD CONSTRAINT credit_has_one_source CHECK (num_nonnulls(source_rerating_job_id, source_adjustment_id, source_credit_note_id, source_invoice_id) = 1);
ALTER TABLE credit_notes DROP COLUMN tax_minor;
