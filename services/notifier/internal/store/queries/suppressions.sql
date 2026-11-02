-- The suppression list (migration 00003). Addresses are stored lower case.

-- name: IsSuppressed :one
SELECT EXISTS (SELECT 1 FROM mail_suppressions WHERE tenant_id = @tenant_id AND address = @address);

-- name: Suppress :execrows
-- The first reason stays: a second bounce of an address that already complained does not rewrite why it is on the list.
INSERT INTO mail_suppressions (tenant_id, address, reason, source, detail)
VALUES (@tenant_id, @address, @reason, @source, @detail)
ON CONFLICT (tenant_id, address) DO NOTHING;

-- name: ListSuppressions :many
SELECT tenant_id, address, reason, source, detail, created_at FROM mail_suppressions
WHERE tenant_id = @tenant_id AND address > @after ORDER BY address LIMIT @page_size;

-- name: Unsuppress :execrows
DELETE FROM mail_suppressions WHERE tenant_id = @tenant_id AND address = @address;
