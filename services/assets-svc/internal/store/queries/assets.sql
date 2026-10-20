-- Assets. Request handlers run these as assets_app inside pgxx.InTenantTx, so RLS scopes every statement to the tenant.

-- name: GetAsset :one
SELECT * FROM assets WHERE id = $1;

-- name: GetAssetForUpdate :one
SELECT * FROM assets WHERE id = $1 FOR UPDATE;

-- name: ListSessionAssets :many
SELECT * FROM assets WHERE session_id = @session_id::uuid ORDER BY created_at, id;

-- name: LockUploadableSessionAssets :many
SELECT * FROM assets
WHERE session_id = @session_id::uuid AND status IN ('PENDING','UPLOADING')
  AND (sqlc.narg('asset_id')::uuid IS NULL OR id = sqlc.narg('asset_id'))
FOR UPDATE;

-- name: ListReadyAssets :many
SELECT * FROM assets
WHERE status = 'READY' AND (@purpose::text = '' OR purpose = @purpose) AND id > @after
ORDER BY id
LIMIT @page_size;

-- name: InsertSessionAsset :exec
INSERT INTO assets (tenant_id, id, session_id, purpose, filename, content_type, declared_size, sha256, object_key, s3_upload_id, part_size, part_count, status)
VALUES (app_tenant_id(), @id, @session_id, @purpose, @filename, @content_type, @declared_size, @sha256, @object_key, @s3_upload_id, @part_size, @part_count, 'PENDING');

-- name: InsertProxiedAsset :exec
INSERT INTO assets (tenant_id, id, purpose, filename, content_type, declared_size, object_key, status)
VALUES (app_tenant_id(), @id, @purpose, @filename, @content_type, @declared_size, @object_key, 'UPLOADING');

-- name: MarkAssetUploading :exec
UPDATE assets SET status = 'UPLOADING' WHERE id = $1;

-- name: SetAssetOutcome :exec
UPDATE assets SET status = @status::text, reject_reason = NULLIF(@reason::text, '') WHERE id = @id;

-- name: RejectAsset :exec
UPDATE assets SET status = 'REJECTED', reject_reason = @reason::text, s3_upload_id = NULL WHERE id = @id;

-- name: MarkAssetReady :exec
UPDATE assets
SET status = 'READY', size_bytes = @size_bytes::bigint, completed_at = @completed_at::timestamptz, s3_upload_id = NULL, sha256 = COALESCE(sqlc.narg('sha256')::bytea, sha256)
WHERE id = @id;

-- name: MarkAssetAborted :exec
UPDATE assets SET status = 'ABORTED', s3_upload_id = NULL WHERE id = $1;

-- name: MarkAssetDeleted :exec
UPDATE assets SET status = 'DELETED' WHERE id = $1;
