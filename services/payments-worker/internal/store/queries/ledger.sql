-- name: AppendLedger :execrows
INSERT INTO payment_ledger (tenant_id, invoice_id, provider, environment, kind, status, amount_minor, currency, provider_ref, idempotency_key, source, detail, occurred_at)
VALUES (@tenant_id, @invoice_id, @provider, @environment, @kind, @status, @amount_minor, @currency, @provider_ref, @idempotency_key, @source, @detail, COALESCE(sqlc.narg('occurred_at')::timestamptz, now()))
ON CONFLICT DO NOTHING;

-- name: ListLedger :many
SELECT id, invoice_id, provider, environment, kind, status, amount_minor, currency, provider_ref, source, detail, occurred_at
FROM payment_ledger WHERE tenant_id = @tenant_id ORDER BY id DESC LIMIT @row_limit::bigint;

-- name: CountLedgerOutcomes :one
SELECT count(DISTINCT status) FROM payment_ledger WHERE tenant_id = @tenant_id AND provider = @provider AND kind = @kind AND provider_ref = @provider_ref AND status <> 'pending';

-- name: GetChargeFact :one
SELECT status, provider_ref, detail FROM payment_ledger
WHERE kind = 'charge' AND idempotency_key = @idempotency_key AND status IN ('succeeded','declined')
ORDER BY (status = 'succeeded') DESC, id DESC LIMIT 1;

-- name: ListSuccessfulCharges :many
SELECT DISTINCT ON (invoice_id, provider, COALESCE(provider_ref, ''))
  invoice_id::text AS invoice_id, provider, COALESCE(provider_ref, '')::text AS provider_ref, currency, amount_minor
FROM payment_ledger WHERE tenant_id = @tenant_id AND kind = 'charge' AND status = 'succeeded' AND invoice_id IS NOT NULL
ORDER BY invoice_id, provider, COALESCE(provider_ref, ''), id;

-- name: ListConflictingCharges :many
SELECT COALESCE(max(invoice_id::text), '')::text AS invoice_id, provider, provider_ref::text AS provider_ref
FROM payment_ledger WHERE tenant_id = @tenant_id AND kind = 'charge' AND provider_ref IS NOT NULL AND status IN ('succeeded', 'declined')
GROUP BY provider, provider_ref HAVING count(DISTINCT status) > 1 ORDER BY provider, provider_ref;

-- name: LookupLedgerByInvoices :many
SELECT id, invoice_id::text AS invoice_id, provider, environment, kind, status, amount_minor, currency, occurred_at
FROM payment_ledger WHERE tenant_id = @tenant_id AND invoice_id = ANY(@invoice_ids::uuid[]) ORDER BY id DESC LIMIT 1000;

-- name: FindOpenCheckout :one
-- The newest checkout opened for an invoice in the last half hour that no notification has settled either way.
SELECT l.provider, l.provider_ref, l.detail, l.idempotency_key FROM payment_ledger l
WHERE l.tenant_id = @tenant_id AND l.invoice_id = @invoice_id AND l.provider = @provider AND l.kind = 'charge' AND l.status = 'pending'
  AND l.detail LIKE 'checkout:%' AND l.occurred_at > @since::timestamptz
  AND NOT EXISTS (SELECT 1 FROM payment_ledger d WHERE d.idempotency_key = l.idempotency_key AND d.status IN ('succeeded', 'declined'))
ORDER BY l.id DESC LIMIT 1;
