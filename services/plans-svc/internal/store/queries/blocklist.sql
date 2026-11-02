-- The blocklists (plan 13). customer_blocklist is the app's (RLS); platform_blocklist is the operator's (the system pool).

-- name: ListBlocklist :many
SELECT id, kind, value, note, created_by, created_at FROM customer_blocklist WHERE tenant_id = app_tenant_id() ORDER BY created_at DESC, id DESC;

-- name: CountBlocklist :one
SELECT count(*) FROM customer_blocklist WHERE tenant_id = app_tenant_id();

-- name: InsertBlocklistEntry :one
INSERT INTO customer_blocklist (tenant_id, id, kind, value, note, created_by) VALUES (app_tenant_id(), @id, @kind, @value, @note, @created_by)
ON CONFLICT (tenant_id, kind, value) DO NOTHING
RETURNING id, kind, value, note, created_by, created_at;

-- name: GetBlocklistEntryByValue :one
SELECT id, kind, value, note, created_by, created_at FROM customer_blocklist WHERE tenant_id = app_tenant_id() AND kind = @kind AND value = @value;

-- name: DeleteBlocklistEntry :one
DELETE FROM customer_blocklist WHERE tenant_id = app_tenant_id() AND id = @id RETURNING kind;

-- name: BlocklistMatches :many
SELECT kind FROM customer_blocklist
WHERE tenant_id = app_tenant_id() AND ((kind = 'email' AND value = @email::text) OR (kind = 'tax_id' AND value = @tax_id::text) OR (kind = 'external_id' AND value = @external_id::text));

-- name: ListPlatformBlocklist :many
SELECT id, kind, value, note, created_by, created_at FROM platform_blocklist ORDER BY created_at DESC, id DESC;

-- name: CountPlatformBlocklist :one
SELECT count(*) FROM platform_blocklist;

-- name: InsertPlatformBlocklistEntry :one
INSERT INTO platform_blocklist (id, kind, value, note, created_by) VALUES (@id, @kind, @value, @note, @created_by)
ON CONFLICT (kind, value) DO NOTHING
RETURNING id, kind, value, note, created_by, created_at;

-- name: GetPlatformBlocklistEntryByValue :one
SELECT id, kind, value, note, created_by, created_at FROM platform_blocklist WHERE kind = @kind AND value = @value;

-- name: DeletePlatformBlocklistEntry :one
DELETE FROM platform_blocklist WHERE id = @id RETURNING kind;

-- name: PlatformBlocklistMatches :many
SELECT kind FROM platform_blocklist
WHERE (kind = 'email' AND value = @email::text) OR (kind = 'tax_id' AND value = @tax_id::text) OR (kind = 'external_id' AND value = @external_id::text);
