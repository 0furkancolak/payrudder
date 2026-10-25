-- Phase 45 E: resetting a sandbox deletes a tenant's customer-side data. billing_system may delete only what the reset lists (queries/reset.sql); a test fails when a
-- table that holds tenant rows is neither listed nor kept on purpose.

-- +goose Up
GRANT DELETE ON credit_applications, credit_ledger, credit_lots, credit_balances, rerating_jobs, saga_events, invoice_sagas, invoice_payments, promotion_reversals,
  einvoice_documents, credit_notes, payment_links, portal_links, invoices, billing_runs, invoice_number_seq, credit_note_seq, tenant_event_outbox, expenses
  TO billing_system;

-- +goose Down
REVOKE DELETE ON credit_applications, credit_ledger, credit_lots, credit_balances, rerating_jobs, saga_events, invoice_sagas, invoice_payments, promotion_reversals,
  einvoice_documents, credit_notes, payment_links, portal_links, invoices, billing_runs, invoice_number_seq, credit_note_seq, tenant_event_outbox, expenses
  FROM billing_system;
