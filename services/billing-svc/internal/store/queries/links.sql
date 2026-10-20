-- Hosted invoice links. Visitors resolve a token through the system pool; tenants manage links through the app pool.

-- name: InsertInvoiceLink :exec
INSERT INTO invoice_links (tenant_id, id, invoice_id, token_hash, expires_at) VALUES (@tenant_id, @id, @invoice_id, @token_hash, @expires_at);

-- name: RevokeInvoiceLinks :execrows
UPDATE invoice_links SET revoked_at = now() WHERE invoice_id = @invoice_id AND revoked_at IS NULL;

-- name: FindActiveInvoiceLink :one
SELECT tenant_id, invoice_id, expires_at FROM invoice_links WHERE token_hash = @token_hash AND revoked_at IS NULL AND expires_at > now();
