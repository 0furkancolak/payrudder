-- Quota accounting (storage_usage), run as assets_app inside pgxx.InTenantTx.

-- name: GetBytesUsed :one
SELECT bytes_used FROM storage_usage WHERE tenant_id = app_tenant_id();

-- name: EnsureStorageUsage :exec
INSERT INTO storage_usage (tenant_id, bytes_used) VALUES (app_tenant_id(), 0) ON CONFLICT DO NOTHING;

-- name: ReserveStorage :execrows
UPDATE storage_usage SET bytes_used = bytes_used + @size::bigint
WHERE tenant_id = app_tenant_id() AND bytes_used + @size::bigint <= @quota::bigint;

-- name: ReleaseStorage :exec
UPDATE storage_usage SET bytes_used = GREATEST(bytes_used - @size::bigint, 0) WHERE tenant_id = app_tenant_id();
