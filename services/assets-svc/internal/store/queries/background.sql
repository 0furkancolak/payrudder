-- The background jobs run as assets_system (BYPASSRLS), so every statement names its tenant.

-- name: ListExpiredSessions :many
SELECT tenant_id, id FROM upload_sessions WHERE status = 'OPEN' AND expires_at < @now ORDER BY expires_at LIMIT @batch;

-- name: LockOpenSessionStatus :one
SELECT status FROM upload_sessions WHERE tenant_id = $1 AND id = $2 FOR UPDATE SKIP LOCKED;

-- name: ListUploadableSessionObjects :many
SELECT id, object_key, s3_upload_id FROM assets WHERE tenant_id = @tenant_id AND session_id = @session_id::uuid AND status IN ('PENDING','UPLOADING');

-- name: AbortTenantAsset :exec
UPDATE assets SET status = 'ABORTED', s3_upload_id = NULL WHERE tenant_id = $1 AND id = $2;

-- name: ExpireSession :exec
UPDATE upload_sessions SET status = 'EXPIRED' WHERE tenant_id = $1 AND id = $2;

-- name: ListUnverifiedAssets :many
SELECT tenant_id, id, object_key, content_type, size_bytes, sha256 FROM assets
WHERE status = 'READY' AND verified_at IS NULL AND session_id IS NOT NULL
ORDER BY created_at
LIMIT @batch;

-- name: MarkAssetVerified :exec
UPDATE assets SET verified_at = @verified_at::timestamptz WHERE tenant_id = @tenant_id AND id = @id;

-- name: MarkAssetCorrupt :exec
UPDATE assets SET status = 'CORRUPT', reject_reason = @reason::text, verified_at = @verified_at::timestamptz WHERE tenant_id = @tenant_id AND id = @id;

-- name: RejectVerifiedAsset :exec
UPDATE assets SET status = 'REJECTED', reject_reason = @reason::text, verified_at = @verified_at::timestamptz WHERE tenant_id = @tenant_id AND id = @id;

-- name: ReleaseTenantStorage :exec
UPDATE storage_usage SET bytes_used = GREATEST(bytes_used - @size::bigint, 0) WHERE tenant_id = @tenant_id;

-- name: ListExistingAssetIDs :many
SELECT id FROM assets WHERE id = ANY(@ids::uuid[]);
