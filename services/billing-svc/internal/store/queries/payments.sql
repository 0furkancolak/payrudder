-- Money received outside a payment provider and recorded by an operator (phase 29).

-- name: LockInvoiceForPayment :one
SELECT status, currency, total_minor FROM invoices WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- name: GetSagaStateByInvoice :one
SELECT id, state FROM invoice_sagas WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id;

-- name: SumInvoicePayments :one
SELECT COALESCE(sum(amount_minor), 0)::bigint AS paid FROM invoice_payments WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id;

-- name: GetInvoicePaymentByReference :one
SELECT id, invoice_id, method, amount_minor, currency, received_at, reference, note, recorded_by, recorded_at
FROM invoice_payments WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id AND method = @method AND reference = @reference;

-- name: InsertInvoicePayment :exec
INSERT INTO invoice_payments (tenant_id, id, invoice_id, method, amount_minor, currency, received_at, reference, note, recorded_by, recorded_at)
VALUES (@tenant_id, @id, @invoice_id, @method, @amount_minor, @currency, @received_at, @reference, @note, @recorded_by, @recorded_at);

-- name: ListInvoicePayments :many
SELECT id, invoice_id, method, amount_minor, currency, received_at, reference, note, recorded_by, recorded_at
FROM invoice_payments WHERE invoice_id = @invoice_id ORDER BY received_at, recorded_at, id;

-- name: LockSagaStateByInvoice :one
SELECT state FROM invoice_sagas WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id FOR UPDATE;
