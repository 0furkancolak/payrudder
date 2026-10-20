-- The mock provider's own database (mockpsp_db).

-- name: BumpCharge :one
UPDATE charges SET requests = requests + 1 WHERE idempotency_key = $1 RETURNING id, status, decline_code;

-- name: CountAttempt :one
INSERT INTO attempts (idempotency_key, requests) VALUES ($1, 1)
ON CONFLICT (idempotency_key) DO UPDATE SET requests = attempts.requests + 1
RETURNING requests;

-- name: InsertCharge :exec
INSERT INTO charges (idempotency_key, id, invoice_id, payment_method, amount_minor, currency, status, decline_code)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (idempotency_key) DO NOTHING;

-- name: GetCharge :one
SELECT id, status, decline_code FROM charges WHERE idempotency_key = $1;

-- name: CountDeclinedCharges :one
SELECT count(*) FROM charges WHERE invoice_id = $1 AND status = 'declined';

-- name: ListInvoiceCharges :many
SELECT idempotency_key, id, status, amount_minor, requests FROM charges WHERE invoice_id = $1 ORDER BY created_at;

-- name: InsertRefund :exec
INSERT INTO refunds (idempotency_key, id, invoice_id, charge_id, amount_minor, currency)
VALUES (@idempotency_key, @id, @invoice_id, NULLIF(@charge_id::text, ''), @amount_minor, @currency)
ON CONFLICT (idempotency_key) DO NOTHING;

-- name: GetRefundID :one
SELECT id FROM refunds WHERE idempotency_key = $1;
