-- name: LockInvoiceReceipts :exec
SELECT pg_advisory_xact_lock(hashtextextended(@invoice_id::text, 29));

-- name: GetLatestChargeAttempt :one
SELECT idempotency_key, provider, environment, created_at,
  COALESCE((request->>'amount_minor')::bigint, (request->>'amountMinor')::bigint, 0)::bigint AS amount_minor,
  COALESCE(request->>'currency', '')::text AS currency,
  COALESCE(request->>'invoice_number', request->>'invoiceNumber', '')::text AS invoice_number
FROM payment_attempts WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id AND kind = 'charge' ORDER BY created_at DESC, command_id LIMIT 1;

-- name: GetPendingTransfer :one
SELECT id, COALESCE(detail, '')::text AS reference, occurred_at FROM payment_ledger
WHERE provider = @provider AND kind = 'charge' AND status = 'pending' AND idempotency_key = @idempotency_key ORDER BY id LIMIT 1;

-- name: GetTransferClosure :one
SELECT id, COALESCE(detail, '')::text AS detail, occurred_at FROM payment_ledger
WHERE provider = @provider AND kind = 'charge' AND status = 'declined' AND idempotency_key = @idempotency_key ORDER BY id DESC LIMIT 1;

-- name: SumManualReceipts :one
SELECT COALESCE(sum(amount_minor), 0)::bigint AS received, COALESCE(max(provider_ref), '')::text AS last_ref FROM payment_ledger
WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id AND provider = @provider AND kind = 'charge' AND status = 'succeeded' AND source = 'manual';

-- name: AppendManualLedger :one
INSERT INTO payment_ledger (tenant_id, invoice_id, provider, environment, kind, status, amount_minor, currency, provider_ref, source, detail, occurred_at)
VALUES (@tenant_id, @invoice_id, @provider, @environment, 'charge', 'succeeded', @amount_minor, @currency, @provider_ref, 'manual', @detail, @occurred_at)
ON CONFLICT DO NOTHING
RETURNING id;

-- name: InsertManualReceipt :exec
INSERT INTO manual_receipts (tenant_id, invoice_id, ledger_id, bank_reference, amount_minor, currency, received_at, note, recorded_by)
VALUES (@tenant_id, @invoice_id, @ledger_id, @bank_reference, @amount_minor, @currency, @received_at, @note, @recorded_by);

-- name: ListManualReceipts :many
SELECT ledger_id, bank_reference, amount_minor, currency, received_at, COALESCE(note, '')::text AS note, recorded_by, recorded_at
FROM manual_receipts WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id ORDER BY received_at, id;

-- name: CloseTransfer :execrows
INSERT INTO payment_ledger (tenant_id, invoice_id, provider, environment, kind, status, amount_minor, currency, provider_ref, idempotency_key, source, detail)
VALUES (@tenant_id, @invoice_id, @provider, @environment, 'charge', 'declined', @amount_minor, @currency, @provider_ref, @idempotency_key, @source, @detail)
ON CONFLICT DO NOTHING;
