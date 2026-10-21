-- Payment links (phase 23 invoice links, extended in phase 34) and customer portal links. Visitors resolve a token through the system pool; tenants manage
-- links through the app pool (RLS). Every system-pool query that reads more than the link row carries the tenant id.

-- name: InsertPaymentLink :exec
INSERT INTO payment_links (tenant_id, id, kind, parent_id, invoice_id, token_hash, expires_at, single_use, idempotency_key, request_hash, amount_minor, currency,
                           description, customer_id, customer_external_id)
VALUES (@tenant_id, @id, @kind, sqlc.narg('parent_id'), sqlc.narg('invoice_id'), @token_hash, @expires_at, @single_use, sqlc.narg('idempotency_key'),
        sqlc.narg('request_hash'), sqlc.narg('amount_minor'), sqlc.narg('currency'), sqlc.narg('description'), sqlc.narg('customer_id'), sqlc.narg('customer_external_id'));

-- name: InsertInvoiceLink :exec
INSERT INTO payment_links (tenant_id, id, kind, invoice_id, token_hash, expires_at) VALUES (@tenant_id, @id, 'invoice', @invoice_id, @token_hash, @expires_at);

-- name: RevokeInvoiceLinks :execrows
UPDATE payment_links SET revoked_at = now() WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id AND revoked_at IS NULL;

-- name: FindActiveInvoiceLink :one
SELECT tenant_id, id, kind, invoice_id, parent_id, single_use, expires_at, amount_minor, currency, description, customer_id, customer_external_id, paid_count
FROM payment_links WHERE token_hash = @token_hash AND revoked_at IS NULL AND expires_at > now();

-- name: GetPaymentLinkRow :one
SELECT id, kind, invoice_id, single_use, expires_at, revoked_at, created_at, amount_minor, currency, description, customer_id, customer_external_id, use_count, paid_count,
       last_paid_at, COALESCE(idempotency_key, '')::text AS idempotency_key,
       (CASE WHEN revoked_at IS NOT NULL THEN 'revoked' WHEN single_use AND paid_count > 0 THEN 'paid' WHEN expires_at <= now() THEN 'expired' ELSE 'active' END)::text AS status
FROM payment_links WHERE tenant_id = @tenant_id AND id = @id AND parent_id IS NULL;

-- name: FindPaymentLinkByKey :one
SELECT id, request_hash FROM payment_links WHERE tenant_id = @tenant_id AND idempotency_key = @idempotency_key::text;

-- name: ListPaymentLinks :many
SELECT * FROM (
  SELECT id, kind, invoice_id, single_use, expires_at, revoked_at, created_at, amount_minor, currency, description, customer_id, customer_external_id, use_count, paid_count,
         last_paid_at, COALESCE(idempotency_key, '')::text AS idempotency_key,
         (CASE WHEN revoked_at IS NOT NULL THEN 'revoked' WHEN single_use AND paid_count > 0 THEN 'paid' WHEN expires_at <= now() THEN 'expired' ELSE 'active' END)::text AS status
  FROM payment_links WHERE tenant_id = @tenant_id AND parent_id IS NULL
) l
WHERE (sqlc.narg('kind')::text IS NULL OR l.kind = sqlc.narg('kind'))
  AND (sqlc.narg('status')::text IS NULL OR l.status = sqlc.narg('status'))
  AND (sqlc.narg('invoice_id')::uuid IS NULL OR l.invoice_id = sqlc.narg('invoice_id'))
  AND (sqlc.narg('customer_external_id')::text IS NULL OR l.customer_external_id = sqlc.narg('customer_external_id'))
  AND (l.created_at, l.id) < (@after_at::timestamptz, @after_id::uuid)
ORDER BY l.created_at DESC, l.id DESC
LIMIT @page_size;

-- name: RevokePaymentLink :execrows
UPDATE payment_links SET revoked_at = now() WHERE tenant_id = @tenant_id AND (id = @id::uuid OR parent_id = @id::uuid) AND revoked_at IS NULL;

-- name: BumpPaymentLinkUse :exec
UPDATE payment_links SET use_count = use_count + 1 WHERE tenant_id = @tenant_id AND id = @id;

-- name: LockPaymentLink :one
SELECT id, single_use, paid_count, use_count FROM payment_links WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- name: FindUnpaidChildInvoice :one
SELECT l.invoice_id FROM payment_links l JOIN invoices i ON i.tenant_id = l.tenant_id AND i.id = l.invoice_id
WHERE l.tenant_id = @tenant_id AND l.parent_id = @id::uuid AND i.status NOT IN ('paid', 'void')
ORDER BY l.created_at DESC LIMIT 1;

-- name: MarkPaymentLinksPaid :many
-- The links an invoice was paid through: its own links, and the parents of its child links. Called by the saga when the invoice is paid.
UPDATE payment_links AS p SET paid_count = p.paid_count + 1, last_paid_at = now()
WHERE p.tenant_id = @tenant_id AND p.parent_id IS NULL AND p.id IN (
  SELECT COALESCE(c.parent_id, c.id) FROM payment_links c WHERE c.tenant_id = @tenant_id AND c.invoice_id = @invoice_id)
RETURNING id, kind, invoice_id, customer_external_id, amount_minor, currency, paid_count;

-- name: ListLinksToExpire :many
SELECT tenant_id, id FROM payment_links
WHERE parent_id IS NULL AND revoked_at IS NULL AND expired_notified_at IS NULL AND expires_at <= now() AND NOT (single_use AND paid_count > 0)
ORDER BY expires_at LIMIT @row_limit;

-- name: LockLinkToExpire :one
SELECT id, kind, invoice_id, customer_external_id, amount_minor, currency, expires_at FROM payment_links
WHERE tenant_id = @tenant_id AND id = @id AND parent_id IS NULL AND revoked_at IS NULL AND expired_notified_at IS NULL AND expires_at <= now()
FOR UPDATE SKIP LOCKED;

-- name: MarkLinkExpiredNotified :exec
UPDATE payment_links SET expired_notified_at = now() WHERE tenant_id = @tenant_id AND id = @id;

-- name: ChildInvoicesToVoid :many
SELECT DISTINCT l.invoice_id::uuid AS invoice_id FROM payment_links l JOIN invoices i ON i.tenant_id = l.tenant_id AND i.id = l.invoice_id
WHERE l.tenant_id = @tenant_id AND l.parent_id = @id::uuid AND i.status = 'open' AND i.kind = 'LINK';

-- name: GetInvoiceForLink :one
SELECT i.id, i.customer_id, i.kind, i.status, i.currency, i.total_minor, COALESCE(i.number, '')::text AS number, i.provider, COALESCE(i.country, '')::text AS country,
       i.snapshot, COALESCE(s.state, '')::text AS saga_state, COALESCE(s.id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS saga_id,
       COALESCE(i.pdf_object_key, '')::text AS pdf_object_key
FROM invoices i LEFT JOIN invoice_sagas s ON s.tenant_id = i.tenant_id AND s.invoice_id = i.id
WHERE i.tenant_id = @tenant_id AND i.id = @id;

-- name: InsertPortalLink :exec
INSERT INTO portal_links (tenant_id, id, customer_id, token_hash, expires_at) VALUES (@tenant_id, @id, @customer_id, @token_hash, @expires_at);

-- name: RevokePortalLinks :execrows
UPDATE portal_links SET revoked_at = now() WHERE tenant_id = @tenant_id AND customer_id = @customer_id AND revoked_at IS NULL;

-- name: FindActivePortalLink :one
SELECT tenant_id, customer_id, expires_at FROM portal_links WHERE token_hash = @token_hash AND revoked_at IS NULL AND expires_at > now();

-- name: ListPortalInvoices :many
SELECT i.id, i.kind, COALESCE(i.number, '')::text AS number, i.status, i.currency, i.total_minor, COALESCE(i.finalized_at, i.created_at)::timestamptz AS issued_at,
       COALESCE(s.state, '')::text AS saga_state, (i.pdf_object_key IS NOT NULL AND i.pdf_object_key <> '')::bool AS pdf_ready,
       COALESCE((i.snapshot->>'currency_exponent')::int, 2)::int AS currency_exponent
FROM invoices i LEFT JOIN invoice_sagas s ON s.tenant_id = i.tenant_id AND s.invoice_id = i.id
WHERE i.tenant_id = @tenant_id AND i.customer_id = @customer_id AND i.status <> 'draft'
ORDER BY COALESCE(i.finalized_at, i.created_at) DESC, i.id DESC
LIMIT @row_limit;

-- name: GetInvoiceOfCustomer :one
SELECT id FROM invoices WHERE tenant_id = @tenant_id AND customer_id = @customer_id AND id = @id AND status <> 'draft';

-- name: SetInvoicePaymentMethod :exec
UPDATE invoices SET snapshot = jsonb_set(snapshot, '{payment_method}', to_jsonb(@payment_method::text)) WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetInvoiceTotals :one
SELECT total_minor, currency, status FROM invoices WHERE tenant_id = @tenant_id AND id = @id;
