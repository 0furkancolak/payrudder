-- Prepaid credits: packs, lots, the ledger (tenant-scoped, billing_app; expiry runs as billing_system).

-- name: InsertCreditPack :one
INSERT INTO credit_packs (tenant_id, id, name, credits, price_minor, currency, validity_days)
VALUES (@tenant_id, @id, @name, @credits, @price_minor, @currency, sqlc.narg('validity_days'))
RETURNING created_at;

-- name: ListCreditPacks :many
SELECT id, name, credits, price_minor, currency, active, created_at, validity_days
FROM credit_packs
WHERE tenant_id = @tenant_id AND (NOT @active_only::boolean OR active)
ORDER BY created_at, id;

-- name: GetCreditPack :one
SELECT id, name, credits, price_minor, currency, active, created_at, validity_days FROM credit_packs WHERE tenant_id = @tenant_id AND id = @id;

-- name: SetCreditPackActive :execrows
UPDATE credit_packs SET active = @active WHERE tenant_id = @tenant_id AND id = @id;

-- One customer's credits change one at a time: the advisory lock is held until the transaction ends.
-- name: LockCustomerCredits :exec
SELECT pg_advisory_xact_lock(hashtextextended((@tenant_id::uuid)::text || '/' || (@customer_id::uuid)::text, 0));

-- name: GetLedgerByKey :many
SELECT id, lot_id, kind, amount, created_at FROM credit_ledger
WHERE tenant_id = @tenant_id AND customer_id = @customer_id AND idempotency_key = @idempotency_key
ORDER BY created_at, id;

-- name: SpendableLots :many
SELECT id, remaining FROM credit_lots
WHERE tenant_id = @tenant_id AND customer_id = @customer_id AND remaining > 0 AND (expires_at IS NULL OR expires_at > now())
ORDER BY expires_at NULLS LAST, created_at, id
FOR UPDATE;

-- name: TakeFromLot :execrows
UPDATE credit_lots SET remaining = remaining - @amount::bigint WHERE tenant_id = @tenant_id AND id = @id AND remaining >= @amount::bigint;

-- name: InsertLot :one
INSERT INTO credit_lots (tenant_id, id, customer_id, granted, remaining, source, source_ref, expires_at)
VALUES (@tenant_id, @id, @customer_id, @granted, @granted, @source, sqlc.narg('source_ref'), sqlc.narg('expires_at'))
ON CONFLICT (tenant_id, source, source_ref) WHERE source_ref IS NOT NULL DO NOTHING
RETURNING id;

-- name: InsertLedger :exec
INSERT INTO credit_ledger (tenant_id, id, customer_id, lot_id, kind, amount, idempotency_key, reason)
VALUES (@tenant_id, @id, @customer_id, @lot_id, @kind, @amount, sqlc.narg('idempotency_key'), @reason);

-- name: GetCreditBalance :one
SELECT COALESCE(sum(remaining) FILTER (WHERE expires_at IS NULL OR expires_at > now()), 0)::bigint AS balance
FROM credit_lots
WHERE tenant_id = @tenant_id AND customer_id = @customer_id;

-- The next time some of the customer's credits run out; no row when none of them expires.
-- name: NextCreditExpiry :one
SELECT expires_at::timestamptz AS expires_at FROM credit_lots
WHERE tenant_id = @tenant_id AND customer_id = @customer_id AND remaining > 0 AND expires_at > now()
ORDER BY expires_at LIMIT 1;

-- name: ListCreditLots :many
SELECT id, granted, remaining, source, source_ref, expires_at, created_at FROM credit_lots
WHERE tenant_id = @tenant_id AND customer_id = @customer_id AND remaining > 0 AND (expires_at IS NULL OR expires_at > now())
ORDER BY expires_at NULLS LAST, created_at, id;

-- name: ListCreditLedger :many
SELECT e.id, e.lot_id, e.kind, e.amount, COALESCE(e.idempotency_key, '')::text AS idempotency_key, e.reason, e.created_at, l.source
FROM credit_ledger e JOIN credit_lots l ON l.tenant_id = e.tenant_id AND l.id = e.lot_id
WHERE e.tenant_id = @tenant_id AND e.customer_id = @customer_id
  AND (sqlc.narg('before_time')::timestamptz IS NULL OR (e.created_at, e.id) < (sqlc.narg('before_time')::timestamptz, sqlc.narg('before_id')::uuid))
ORDER BY e.created_at DESC, e.id DESC
LIMIT @row_limit;

-- Expiry (billing_system, every tenant): lots whose time has come and that still hold credits.
-- name: ExpiredLots :many
SELECT tenant_id, id, customer_id, remaining FROM credit_lots
WHERE remaining > 0 AND expires_at <= now()
ORDER BY expires_at, id
LIMIT @row_limit
FOR UPDATE SKIP LOCKED;

-- name: ZeroLot :exec
UPDATE credit_lots SET remaining = 0 WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetCreditSettings :one
SELECT low_threshold FROM credit_settings WHERE tenant_id = @tenant_id;

-- name: UpsertCreditSettings :exec
INSERT INTO credit_settings (tenant_id, low_threshold) VALUES (@tenant_id, @low_threshold)
ON CONFLICT (tenant_id) DO UPDATE SET low_threshold = EXCLUDED.low_threshold, updated_at = now();
