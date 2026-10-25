-- Resetting a sandbox tenant (phase 45 E): what goes is everything that came from the tenant's customers (invoices and what hangs on them, credits, numbers, runs,
-- pending commands). What stays is configuration: credit packs, credit settings, dunning settings. Run as billing_system from internal/sandboxreset only; every
-- statement names the tenant. The order is the order of the foreign keys: children before parents.

-- name: ResetCreditApplications :execrows
DELETE FROM credit_applications WHERE tenant_id = $1;
-- name: ResetCreditLedger :execrows
DELETE FROM credit_ledger WHERE tenant_id = $1;
-- name: ResetCreditLots :execrows
DELETE FROM credit_lots WHERE tenant_id = $1;
-- name: ResetCreditBalances :execrows
DELETE FROM credit_balances WHERE tenant_id = $1;
-- name: ResetReratingJobs :execrows
DELETE FROM rerating_jobs WHERE tenant_id = $1;
-- name: ResetSagaEvents :execrows
DELETE FROM saga_events WHERE tenant_id = $1;
-- name: ResetInvoiceSagas :execrows
DELETE FROM invoice_sagas WHERE tenant_id = $1;
-- name: ResetInvoiceLines :execrows
DELETE FROM invoice_lines WHERE tenant_id = $1;
-- name: ResetInvoicePayments :execrows
DELETE FROM invoice_payments WHERE tenant_id = $1;
-- name: ResetPromotionReversals :execrows
DELETE FROM promotion_reversals WHERE tenant_id = $1;
-- name: ResetEInvoiceDocuments :execrows
DELETE FROM einvoice_documents WHERE tenant_id = $1;
-- name: ResetCreditNotes :execrows
DELETE FROM credit_notes WHERE tenant_id = $1;
-- A link may point at its parent (a reusable link and the invoices made from it): children first.
-- name: ResetPaymentLinkChildren :execrows
DELETE FROM payment_links WHERE tenant_id = $1 AND parent_id IS NOT NULL;
-- name: ResetPaymentLinks :execrows
DELETE FROM payment_links WHERE tenant_id = $1;
-- name: ResetPortalLinks :execrows
DELETE FROM portal_links WHERE tenant_id = $1;
-- name: ResetInvoices :execrows
DELETE FROM invoices WHERE tenant_id = $1;
-- name: ResetBillingRuns :execrows
DELETE FROM billing_runs WHERE tenant_id = $1;
-- Numbering starts again, at SBX-...000001.
-- name: ResetInvoiceNumberSeq :execrows
DELETE FROM invoice_number_seq WHERE tenant_id = $1;
-- name: ResetCreditNoteSeq :execrows
DELETE FROM credit_note_seq WHERE tenant_id = $1;
-- name: ResetOutbox :execrows
DELETE FROM outbox WHERE tenant_id = $1;
-- name: ResetTenantEventOutbox :execrows
DELETE FROM tenant_event_outbox WHERE tenant_id = $1;
-- name: ResetExpenses :execrows
DELETE FROM expenses WHERE tenant_id = $1;
