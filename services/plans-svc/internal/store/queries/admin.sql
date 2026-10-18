-- Operator and gateway queries, run as plans_system (BYPASSRLS) from internal/admin only.

-- name: InsertAccount :one
INSERT INTO accounts (id, name) VALUES ($1, $2) RETURNING *;

-- name: InsertTenant :one
INSERT INTO tenants (id, account_id, environment, name, tier, currency, timezone, grace_period)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetTenant :one
SELECT * FROM tenants WHERE id = $1;

-- name: ListAccountTenants :many
SELECT * FROM tenants WHERE account_id = $1 ORDER BY environment DESC;

-- name: UpdateTenantSettings :one
UPDATE tenants
SET grace_period = COALESCE(sqlc.narg(grace_period)::interval, grace_period),
    tier         = COALESCE(sqlc.narg(tier)::text, tier)
WHERE id = @id
RETURNING *;

-- name: InsertApiKey :one
INSERT INTO api_keys (id, tenant_id, prefix, key_hash, env, status)
VALUES ($1, $2, $3, $4, $5, 'active')
RETURNING *;

-- name: GetApiKey :one
SELECT * FROM api_keys WHERE id = $1;

-- name: RevokeApiKey :one
UPDATE api_keys SET status = 'revoked', revoked_at = COALESCE(revoked_at, now())
WHERE id = $1
RETURNING *;

-- name: ResolveApiKey :one
SELECT k.id, k.tenant_id, k.key_hash, k.status, k.env, t.environment, t.tier, t.timezone
FROM api_keys k JOIN tenants t ON t.id = k.tenant_id
WHERE k.prefix = $1;

-- name: ListTenants :many
SELECT * FROM tenants WHERE id > @after::uuid ORDER BY id LIMIT @page_size::int;
