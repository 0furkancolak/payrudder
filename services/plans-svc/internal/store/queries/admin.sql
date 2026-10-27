-- Operator and gateway queries, run as plans_system (BYPASSRLS) from internal/admin only.

-- name: InsertAccount :one
INSERT INTO accounts (id, name) VALUES ($1, $2) RETURNING *;

-- name: InsertTenant :one
INSERT INTO tenants (id, account_id, app_id, environment, name, tier, currency, timezone, grace_period, status, activated_at)
VALUES (@id, @account_id, @app_id, @environment, @name, @tier, @currency, @timezone, @grace_period, @status, CASE WHEN @status::text = 'active' THEN now() END)
RETURNING *;

-- name: SetTenantStatus :one
UPDATE tenants SET status = @status::text, activated_at = CASE WHEN @status::text = 'active' THEN COALESCE(activated_at, now()) ELSE activated_at END
WHERE id = @id
RETURNING *;

-- name: InsertApp :one
INSERT INTO apps (id, account_id, name, slug) VALUES ($1, $2, $3, $4) RETURNING *;

-- The apps an account shows: an archived (deleted) app is not one of them.
-- name: ListAccountApps :many
SELECT * FROM apps WHERE account_id = $1 AND archived_at IS NULL ORDER BY created_at, id;

-- The operator also sees the archived apps (they keep their billing records).
-- name: ListAccountAppsWithArchived :many
SELECT * FROM apps WHERE account_id = $1 ORDER BY created_at, id;

-- name: GetApp :one
SELECT * FROM apps WHERE id = $1;

-- Locks the app for the rest of the transaction, so a rename and a delete (or two deletes) of one app are taken one after the other.
-- name: LockApp :one
SELECT * FROM apps WHERE id = $1 FOR UPDATE;

-- name: RenameApp :one
UPDATE apps SET name = @name WHERE id = @id AND archived_at IS NULL RETURNING *;

-- The tenants carry the app's name (the sandbox with its suffix): it is what the seller line of a new invoice says. Issued invoices keep their snapshot.
-- name: RenameAppTenants :exec
UPDATE tenants SET name = CASE WHEN environment = 'sandbox' THEN @name::text || ' (sandbox)' ELSE @name::text END
WHERE app_id = @app_id;

-- Archiving frees the slug (a new app may take it): the old one is kept recognisable, and unique through the app's own id.
-- name: ArchiveApp :one
UPDATE apps
SET archived_at = now(), archived_by = @user_id,
    slug = rtrim(left(slug, 36), '-') || '-archived-' || right(replace(id::text, '-', ''), 12)
WHERE id = @id AND archived_at IS NULL
RETURNING *;

-- name: RevokeAppApiKeys :many
UPDATE api_keys k SET status = 'revoked', revoked_at = COALESCE(k.revoked_at, now())
FROM tenants t
WHERE t.id = k.tenant_id AND t.app_id = @app_id AND k.status = 'active'
RETURNING k.*;

-- name: InsertAppAudit :exec
INSERT INTO app_audit (id, app_id, user_id, action, detail) VALUES (@id, @app_id, @user_id, @action, @detail::jsonb);

-- name: GetAccount :one
SELECT * FROM accounts WHERE id = $1;

-- name: GetTenant :one
SELECT * FROM tenants WHERE id = $1;

-- name: ListAccountTenants :many
SELECT * FROM tenants WHERE account_id = $1 ORDER BY environment DESC;

-- name: UpdateTenantSettings :one
UPDATE tenants
SET grace_period = COALESCE(sqlc.narg(grace_period)::interval, grace_period),
    tier         = COALESCE(sqlc.narg(tier)::text, tier),
    address      = COALESCE(sqlc.narg(address)::jsonb, address),
    legal        = COALESCE(sqlc.narg(legal)::jsonb, legal),
    e_invoice_on = COALESCE(sqlc.narg(e_invoice_on)::text, e_invoice_on),
    bank_transfer = CASE WHEN @clear_bank_transfer::bool THEN '{}'::jsonb ELSE COALESCE(sqlc.narg(bank_transfer)::jsonb, bank_transfer) END,
    invoice_logo_asset_id = CASE WHEN @clear_logo::bool THEN NULL ELSE COALESCE(sqlc.narg(invoice_logo_asset_id)::uuid, invoice_logo_asset_id) END
WHERE id = @id
RETURNING *;

-- name: InsertApiKey :one
INSERT INTO api_keys (id, tenant_id, prefix, key_hash, env, status, role, permissions)
VALUES (@id, @tenant_id, @prefix, @key_hash, @env, 'active', @role, @permissions::text[])
RETURNING *;

-- The old key of a rotation keeps working until expires_at (never extended: a key that ends sooner stays at the sooner time).
-- name: ExpireApiKey :one
UPDATE api_keys SET expires_at = LEAST(COALESCE(expires_at, @expires_at::timestamptz), @expires_at::timestamptz)
WHERE id = @id AND status = 'active'
RETURNING *;

-- A use is written at most every few minutes (the gateway throttles), and never moves the time back.
-- name: TouchApiKey :exec
UPDATE api_keys SET last_used_at = now() WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: ListTenantApiKeyPrefixes :many
SELECT prefix FROM api_keys WHERE tenant_id = $1;

-- name: GetApiKey :one
SELECT * FROM api_keys WHERE id = $1;

-- name: RevokeApiKey :one
UPDATE api_keys SET status = 'revoked', revoked_at = COALESCE(revoked_at, now())
WHERE id = $1
RETURNING *;

-- name: ResolveApiKey :one
SELECT k.id, k.tenant_id, k.key_hash, k.status, k.env, k.role, k.permissions, k.expires_at, t.environment, t.status AS tenant_status, t.tier, t.timezone
FROM api_keys k JOIN tenants t ON t.id = k.tenant_id
WHERE k.prefix = $1;

-- name: ListTenants :many
SELECT * FROM tenants WHERE id > @after::uuid ORDER BY id LIMIT @page_size::int;
