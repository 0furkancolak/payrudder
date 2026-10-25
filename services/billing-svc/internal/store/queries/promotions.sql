-- Promotion codes on invoices (phase 46). The coupon and its redemption are plans-svc's; the invoice remembers which it used.

-- name: SetInvoicePromotion :exec
UPDATE invoices SET coupon_code = @coupon_code, promotion_code = sqlc.narg('promotion_code'), promotion_target = @promotion_target, bonus_credits = @bonus_credits
WHERE tenant_id = @tenant_id AND id = @id;

-- name: CustomerHasPaidInvoice :one
-- "First purchase" for a promotion code: the customer has paid for nothing yet.
SELECT EXISTS (SELECT 1 FROM invoices WHERE tenant_id = @tenant_id AND customer_id = @customer_id AND status = 'paid')::boolean;

-- name: GetInvoiceBonusCredits :one
SELECT bonus_credits FROM invoices WHERE tenant_id = @tenant_id AND id = @id;

-- name: QueuePromotionReversal :exec
-- A voided invoice that used a coupon: plans-svc is told in the next scheduler pass. Queuing twice is one reversal.
INSERT INTO promotion_reversals (tenant_id, invoice_id, target_kind)
SELECT i.tenant_id, i.id, i.promotion_target FROM invoices i WHERE i.tenant_id = @tenant_id AND i.id = @id AND i.promotion_target IS NOT NULL
ON CONFLICT DO NOTHING;

-- name: ListPendingPromotionReversals :many
SELECT tenant_id, invoice_id, target_kind FROM promotion_reversals WHERE done_at IS NULL ORDER BY created_at, invoice_id LIMIT @row_limit;

-- name: MarkPromotionReversalDone :exec
UPDATE promotion_reversals SET done_at = now(), attempts = attempts + 1 WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id;

-- name: BumpPromotionReversalAttempts :exec
UPDATE promotion_reversals SET attempts = attempts + 1 WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id;
