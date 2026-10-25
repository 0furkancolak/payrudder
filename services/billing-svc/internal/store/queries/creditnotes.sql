-- Credit notes, their refunds and the credit balances customers draw from.

-- name: GetCreditNote :one
SELECT c.id, c.customer_id, c.invoice_id, c.number, COALESCE(i.number, '')::text AS invoice_number, c.currency, c.total_minor, c.reason, c.created_at,
       COALESCE(i.snapshot->>'customer_external_id', '')::text AS customer_external_id, (c.disposition = 'refund')::bool AS refund, c.refund_state,
       COALESCE(c.refund_error, '')::text AS refund_error
FROM credit_notes c JOIN invoices i ON i.tenant_id = c.tenant_id AND i.id = c.invoice_id
WHERE c.id = @id;

-- name: ListCreditNotes :many
SELECT c.id, c.customer_id, c.invoice_id, c.number, COALESCE(i.number, '')::text AS invoice_number, c.currency, c.total_minor, c.reason, c.created_at,
       COALESCE(i.snapshot->>'customer_external_id', '')::text AS customer_external_id, (c.disposition = 'refund')::bool AS refund, c.refund_state,
       COALESCE(c.refund_error, '')::text AS refund_error
FROM credit_notes c JOIN invoices i ON i.tenant_id = c.tenant_id AND i.id = c.invoice_id
WHERE (sqlc.narg('invoice_id')::uuid IS NULL OR c.invoice_id = sqlc.narg('invoice_id'))
  AND (sqlc.narg('customer_external_id')::text IS NULL OR i.snapshot->>'customer_external_id' = sqlc.narg('customer_external_id'))
  AND (c.created_at, c.id) < (@after_at::timestamptz, @after_id::uuid)
ORDER BY c.created_at DESC, c.id DESC
LIMIT @page_size;

-- name: LockInvoiceForCreditNote :one
SELECT status, currency, COALESCE(snapshot->>'environment', 'live')::text AS environment, number, total_minor, customer_id
FROM invoices WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- name: SumCreditNotes :one
SELECT COALESCE(sum(total_minor), 0)::bigint AS credited FROM credit_notes WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id;

-- name: EnsureCreditNoteSeq :exec
INSERT INTO credit_note_seq (tenant_id, year, next_value) VALUES (@tenant_id, @year, 1) ON CONFLICT DO NOTHING;

-- name: LockCreditNoteSeq :one
SELECT next_value FROM credit_note_seq WHERE tenant_id = @tenant_id AND year = @year FOR UPDATE;

-- name: BumpCreditNoteSeq :exec
UPDATE credit_note_seq SET next_value = next_value + 1 WHERE tenant_id = @tenant_id AND year = @year;

-- name: InsertCreditNote :exec
INSERT INTO credit_notes (tenant_id, id, customer_id, invoice_id, number, currency, total_minor, reason, created_at, disposition)
VALUES (@tenant_id, @id, @customer_id, @invoice_id, @number, @currency, @total_minor, @reason, @created_at, @disposition);

-- name: MarkRefundRequested :exec
UPDATE credit_notes SET refund_state = 'requested', refund_attempt = @refund_attempt, refund_command_id = @refund_command_id::uuid,
       refund_requested_at = @refund_requested_at::timestamptz, refund_error = NULL, refund_run_from = @refund_run_from, refund_generation = @refund_generation
WHERE tenant_id = @tenant_id AND id = @id;

-- name: LockCreditNoteByRefundCommand :one
SELECT id, customer_id, invoice_id, number, currency, total_minor, COALESCE(reason, '')::text AS reason, created_at FROM credit_notes
WHERE tenant_id = @tenant_id AND refund_command_id = @refund_command_id::uuid AND refund_state = 'requested'
FOR UPDATE;

-- name: RefundCommandExists :one
SELECT EXISTS (SELECT 1 FROM credit_notes WHERE tenant_id = @tenant_id AND refund_command_id = @refund_command_id::uuid);

-- name: MarkRefundRefunded :exec
UPDATE credit_notes SET refund_state = 'refunded', refund_ref = @refund_ref::text, refund_error = NULL WHERE tenant_id = @tenant_id AND id = @id;

-- name: MarkRefundFailed :exec
UPDATE credit_notes SET refund_state = 'failed', refund_error = @refund_error::text WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListRefundsAwaitingReply :many
SELECT c.tenant_id, c.id, c.invoice_id, c.currency, c.total_minor, c.refund_attempt, c.refund_run_from, c.refund_generation,
       COALESCE(i.snapshot->>'environment', 'live')::text AS environment
FROM credit_notes c JOIN invoices i ON i.tenant_id = c.tenant_id AND i.id = c.invoice_id
WHERE c.refund_state = 'requested' AND c.refund_requested_at <= @requested_before::timestamptz
ORDER BY c.refund_requested_at LIMIT 100;

-- name: LockCreditNoteRefundState :one
SELECT refund_state, refund_attempt FROM credit_notes WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- name: GetCreditNoteForEvent :one
SELECT number, COALESCE(reason, '')::text AS reason, customer_id FROM credit_notes WHERE tenant_id = @tenant_id AND id = @id;

-- name: LockRefundForRetry :one
SELECT c.invoice_id, c.customer_id, c.number, c.currency, c.total_minor, c.reason, c.created_at, c.refund_state, c.refund_attempt, c.refund_generation,
       COALESCE(c.refund_error, '')::text AS refund_error, COALESCE(i.snapshot->>'environment', 'live')::text AS environment
FROM credit_notes c JOIN invoices i ON i.tenant_id = c.tenant_id AND i.id = c.invoice_id
WHERE c.tenant_id = @tenant_id AND c.id = @id AND c.disposition = 'refund'
FOR UPDATE OF c;

-- name: InsertCreditBalanceForNote :exec
INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_credit_note_id, currency)
VALUES (@tenant_id, @customer_id, @id, @amount_minor, @source_credit_note_id::uuid, @currency::text);

-- name: InsertCreditBalanceForAdjustment :exec
INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_adjustment_id, currency)
VALUES (@tenant_id, @customer_id, @id, @amount_minor, @source_adjustment_id::uuid, @currency::text)
ON CONFLICT (tenant_id, source_adjustment_id) WHERE source_adjustment_id IS NOT NULL DO NOTHING;

-- name: InsertCreditBalanceForRerating :exec
INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_rerating_job_id)
VALUES (@tenant_id, @customer_id, @id, @amount_minor, @source_rerating_job_id::uuid)
ON CONFLICT (tenant_id, customer_id, source_rerating_job_id) DO NOTHING;

-- name: ListUnappliedCredits :many
SELECT c.id, (c.amount_minor - COALESCE((SELECT sum(a.amount_minor) FROM credit_applications a WHERE a.tenant_id = c.tenant_id AND a.credit_id = c.id), 0))::bigint AS remaining
FROM credit_balances c
WHERE c.tenant_id = @tenant_id AND c.customer_id = @customer_id AND c.applied_invoice_id IS NULL AND (c.currency = @currency::text OR c.currency IS NULL)
ORDER BY c.created_at, c.id
FOR UPDATE OF c;

-- name: InsertCreditApplication :exec
INSERT INTO credit_applications (tenant_id, credit_id, invoice_id, amount_minor) VALUES (@tenant_id, @credit_id, @invoice_id, @amount_minor);

-- name: MarkCreditApplied :exec
UPDATE credit_balances SET applied_invoice_id = @applied_invoice_id::uuid WHERE tenant_id = @tenant_id AND id = @id;

-- name: DeleteCreditApplicationsOfInvoice :exec
DELETE FROM credit_applications WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id;

-- name: ClearCreditsAppliedToInvoice :exec
UPDATE credit_balances SET applied_invoice_id = NULL WHERE tenant_id = @tenant_id AND applied_invoice_id = @invoice_id::uuid;
