-- The invoice inbox of a tenant (expense_inboxes is read by token without a tenant), the attachments received by mail, and the recurring-vendor rules.

-- name: GetInboxByScope :one
SELECT token FROM expense_inboxes WHERE tenant_id = @tenant_id AND scope = @scope;

-- name: InsertInbox :one
INSERT INTO expense_inboxes (tenant_id, scope, token) VALUES (@tenant_id, @scope, @token)
ON CONFLICT (tenant_id, scope) DO UPDATE SET token = expense_inboxes.token
RETURNING token;

-- name: RotateInbox :one
UPDATE expense_inboxes SET token = @token WHERE tenant_id = @tenant_id AND scope = @scope RETURNING token;

-- name: ResolveInboxToken :one
SELECT tenant_id, scope FROM expense_inboxes WHERE token = @token;

-- name: InsertInboxItem :exec
INSERT INTO expense_inbox_items (tenant_id, id, app_id, from_addr, subject, filename, content_type, size_bytes, content)
VALUES (@tenant_id, @id, sqlc.narg('app_id'), @from_addr, @subject, @filename, @content_type, @size_bytes, @content);

-- name: CountInboxToReview :one
SELECT count(*)::bigint FROM expense_inbox_items WHERE tenant_id = @tenant_id AND status = 'to_review';

-- name: ListInboxItems :many
SELECT id, app_id, from_addr, subject, filename, content_type, size_bytes, status, expense_id, received_at
FROM expense_inbox_items WHERE tenant_id = @tenant_id AND status = @status
ORDER BY received_at DESC, id DESC LIMIT @row_limit;

-- name: GetInboxItemFile :one
SELECT filename, content_type, content FROM expense_inbox_items WHERE tenant_id = @tenant_id AND id = @id;

-- An item is resolved once.
-- name: ResolveInboxItem :one
UPDATE expense_inbox_items SET status = @status, expense_id = sqlc.narg('expense_id'), resolved_at = now()
WHERE tenant_id = @tenant_id AND id = @id AND status = 'to_review'
RETURNING id, app_id, from_addr, subject, filename, content_type, size_bytes, status, expense_id, received_at;

-- name: ListExpenseRules :many
SELECT id, app_id, vendor_match, category, currency, tax_bps, expected_amount_minor, tolerance_bps, auto_approve
FROM expense_rules WHERE tenant_id = @tenant_id ORDER BY lower(vendor_match), id;

-- name: CountExpenseRules :one
SELECT count(*)::bigint FROM expense_rules WHERE tenant_id = @tenant_id;

-- name: InsertExpenseRule :exec
INSERT INTO expense_rules (tenant_id, id, app_id, vendor_match, category, currency, tax_bps, expected_amount_minor, tolerance_bps, auto_approve)
VALUES (@tenant_id, @id, sqlc.narg('app_id'), @vendor_match, @category, @currency, @tax_bps, @expected_amount_minor, @tolerance_bps, @auto_approve);

-- name: UpdateExpenseRule :one
UPDATE expense_rules SET app_id = sqlc.narg('app_id'), vendor_match = @vendor_match, category = @category, currency = @currency, tax_bps = @tax_bps,
       expected_amount_minor = @expected_amount_minor, tolerance_bps = @tolerance_bps, auto_approve = @auto_approve
WHERE tenant_id = @tenant_id AND id = @id RETURNING id;

-- name: DeleteExpenseRule :execrows
DELETE FROM expense_rules WHERE tenant_id = @tenant_id AND id = @id;
