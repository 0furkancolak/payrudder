-- Upload sessions. Request handlers run these as assets_app inside pgxx.InTenantTx, so RLS scopes every statement to the tenant.

-- name: GetUploadSession :one
SELECT * FROM upload_sessions WHERE id = $1;

-- name: GetUploadSessionForUpdate :one
SELECT * FROM upload_sessions WHERE id = $1 FOR UPDATE;

-- name: GetSessionIDByIdempotencyKey :one
SELECT id FROM upload_sessions WHERE idempotency_key = @idempotency_key::text;

-- name: InsertUploadSession :execrows
INSERT INTO upload_sessions (tenant_id, id, idempotency_key, status, expires_at)
VALUES (app_tenant_id(), @id, sqlc.narg('idempotency_key')::text, 'OPEN', @expires_at)
ON CONFLICT DO NOTHING;

-- name: CompleteSessionIfDrained :exec
UPDATE upload_sessions SET status = 'COMPLETED'
WHERE upload_sessions.id = $1 AND upload_sessions.status = 'OPEN'
  AND NOT EXISTS (SELECT 1 FROM assets WHERE session_id = $1 AND status IN ('PENDING','UPLOADING'));

-- name: SettleSession :exec
UPDATE upload_sessions SET status = @status::text
WHERE upload_sessions.id = @id AND upload_sessions.status = 'OPEN'
  AND (@force::bool OR NOT EXISTS (SELECT 1 FROM assets WHERE session_id = @id AND status IN ('PENDING','UPLOADING')));
