-- Coupons, promotion codes and the redemption ledger (phase 46). Every statement names the tenant through app_tenant_id(); RLS binds it as well.

-- name: InsertCoupon :one
INSERT INTO coupons (tenant_id, code, name, percent_bps, amount_minor, currency, bonus_percent_bps, applies_to, duration, duration_periods, max_redemptions, expires_at)
VALUES (app_tenant_id(), @code, @name, @percent_bps, @amount_minor, @currency, @bonus_percent_bps, @applies_to::text[], @duration, @duration_periods, @max_redemptions, @expires_at)
RETURNING id, code, name, percent_bps, amount_minor, currency, bonus_percent_bps, applies_to, duration, duration_periods, max_redemptions, redeemed, expires_at, active, created_at,
  0::int AS promotion_code_count;

-- name: ListCoupons :many
SELECT c.id, c.code, c.name, c.percent_bps, c.amount_minor, c.currency, c.bonus_percent_bps, c.applies_to, c.duration, c.duration_periods, c.max_redemptions, c.redeemed,
  c.expires_at, c.active, c.created_at,
  (SELECT count(*) FROM promotion_codes p WHERE p.tenant_id = c.tenant_id AND p.coupon_id = c.id)::int AS promotion_code_count
FROM coupons c WHERE c.tenant_id = app_tenant_id() ORDER BY c.created_at DESC, c.code;

-- name: GetCoupon :one
SELECT c.id, c.code, c.name, c.percent_bps, c.amount_minor, c.currency, c.bonus_percent_bps, c.applies_to, c.duration, c.duration_periods, c.max_redemptions, c.redeemed,
  c.expires_at, c.active, c.created_at,
  (SELECT count(*) FROM promotion_codes p WHERE p.tenant_id = c.tenant_id AND p.coupon_id = c.id)::int AS promotion_code_count
FROM coupons c WHERE c.tenant_id = app_tenant_id() AND c.code = $1;

-- name: GetCouponByID :one
SELECT c.id, c.code, c.name, c.percent_bps, c.amount_minor, c.currency, c.bonus_percent_bps, c.applies_to, c.duration, c.duration_periods, c.max_redemptions, c.redeemed,
  c.expires_at, c.active, c.created_at,
  (SELECT count(*) FROM promotion_codes p WHERE p.tenant_id = c.tenant_id AND p.coupon_id = c.id)::int AS promotion_code_count
FROM coupons c WHERE c.tenant_id = app_tenant_id() AND c.id = $1;

-- name: DeactivateCoupon :execrows
UPDATE coupons SET active = false WHERE tenant_id = app_tenant_id() AND code = $1;

-- name: ClaimCoupon :execrows
-- Counts a redemption if the coupon can still be redeemed. One statement, so two redemptions cannot both take the last one: the second waits for the first's
-- row lock and then sees the new count.
UPDATE coupons SET redeemed = redeemed + 1
WHERE tenant_id = app_tenant_id() AND id = @id AND active
  AND (max_redemptions IS NULL OR redeemed < max_redemptions) AND (expires_at IS NULL OR expires_at > @now::timestamptz);

-- name: ReturnCouponSlot :exec
UPDATE coupons SET redeemed = redeemed - 1 WHERE tenant_id = app_tenant_id() AND id = $1 AND redeemed > 0;

-- name: InsertSubscriptionCoupon :exec
INSERT INTO subscription_coupons (tenant_id, subscription_id, coupon_code, redemption_id) VALUES (app_tenant_id(), @subscription_id, @coupon_code, @redemption_id);

-- name: CountOtherSubscriptions :one
-- The customer's other subscriptions, ended or not: "first purchase" for a subscription means there is none.
SELECT count(*)::int FROM subscriptions WHERE tenant_id = app_tenant_id() AND customer_id = @customer_id AND id <> @except_id;

-- name: AddRedemptionDiscount :exec
-- A subscription's redemption grows by what the coupon takes off each fee.
UPDATE coupon_redemptions r SET discount_minor = r.discount_minor + @discount::bigint
FROM subscription_coupons sc
WHERE sc.tenant_id = app_tenant_id() AND sc.subscription_id = @subscription_id AND r.tenant_id = sc.tenant_id AND r.id = sc.redemption_id;

-- name: InsertPromotionCodes :many
-- Many codes with one set of rules; a code that exists already (in any case) is skipped, and the caller sees which went in.
INSERT INTO promotion_codes (tenant_id, coupon_id, code, max_redemptions, expires_at, first_purchase_only, one_per_customer, min_amount_minor, min_amount_currency,
  allowed_plans, allowed_packs, allowed_currencies, restore_on_reversal)
SELECT app_tenant_id(), @coupon_id::uuid, c, sqlc.narg('max_redemptions')::int, sqlc.narg('expires_at')::timestamptz, @first_purchase_only::boolean, @one_per_customer::boolean, sqlc.narg('min_amount_minor')::bigint, sqlc.narg('min_amount_currency')::char(3),
  @allowed_plans::text[], @allowed_packs::text[], @allowed_currencies::text[], @restore_on_reversal::boolean
FROM unnest(@codes::text[]) AS c
ON CONFLICT DO NOTHING
RETURNING id;

-- name: GetPromotionCodeByText :one
SELECT p.id, p.coupon_id, c.code AS coupon_code, p.code, p.active, p.max_redemptions, p.redeemed, p.expires_at, p.first_purchase_only, p.one_per_customer,
  p.min_amount_minor, p.min_amount_currency, p.allowed_plans, p.allowed_packs, p.allowed_currencies, p.restore_on_reversal, p.created_at
FROM promotion_codes p JOIN coupons c ON c.tenant_id = p.tenant_id AND c.id = p.coupon_id
WHERE p.tenant_id = app_tenant_id() AND upper(p.code) = upper(@code::text);

-- name: ListPromotionCodesByID :many
SELECT p.id, p.coupon_id, c.code AS coupon_code, p.code, p.active, p.max_redemptions, p.redeemed, p.expires_at, p.first_purchase_only, p.one_per_customer,
  p.min_amount_minor, p.min_amount_currency, p.allowed_plans, p.allowed_packs, p.allowed_currencies, p.restore_on_reversal, p.created_at
FROM promotion_codes p JOIN coupons c ON c.tenant_id = p.tenant_id AND c.id = p.coupon_id
WHERE p.tenant_id = app_tenant_id() AND p.id = ANY(@ids::uuid[]) ORDER BY p.code;

-- name: ListPromotionCodes :many
SELECT p.id, p.coupon_id, c.code AS coupon_code, p.code, p.active, p.max_redemptions, p.redeemed, p.expires_at, p.first_purchase_only, p.one_per_customer,
  p.min_amount_minor, p.min_amount_currency, p.allowed_plans, p.allowed_packs, p.allowed_currencies, p.restore_on_reversal, p.created_at,
  count(*) OVER()::bigint AS _count
FROM promotion_codes p JOIN coupons c ON c.tenant_id = p.tenant_id AND c.id = p.coupon_id
WHERE p.tenant_id = app_tenant_id()
  AND (sqlc.narg('coupon_code')::text IS NULL OR c.code = sqlc.narg('coupon_code'))
  AND (sqlc.narg('active')::boolean IS NULL OR p.active = sqlc.narg('active'))
  AND (sqlc.narg('search')::text IS NULL OR p.code ILIKE '%' || sqlc.narg('search') || '%')
ORDER BY p.created_at DESC, p.code
LIMIT @row_limit OFFSET @row_offset;

-- name: DeactivatePromotionCode :execrows
UPDATE promotion_codes SET active = false WHERE tenant_id = app_tenant_id() AND upper(code) = upper(@code::text);

-- name: ClaimPromotionCode :execrows
-- The same conditional update as ClaimCoupon, for one code. It holds the row's lock until the transaction ends, which also serialises the checks that look at
-- what a customer has used of the code.
UPDATE promotion_codes SET redeemed = redeemed + 1
WHERE tenant_id = app_tenant_id() AND id = @id AND active
  AND (max_redemptions IS NULL OR redeemed < max_redemptions) AND (expires_at IS NULL OR expires_at > @now::timestamptz);

-- name: ReturnPromotionCodeSlot :exec
UPDATE promotion_codes SET redeemed = redeemed - 1 WHERE tenant_id = app_tenant_id() AND id = $1 AND redeemed > 0;

-- name: CustomerCodeUses :one
-- How often the customer has used the code: a reversed redemption counts unless it gave its place back.
SELECT count(*)::int FROM coupon_redemptions
WHERE tenant_id = app_tenant_id() AND promotion_code_id = @promotion_code_id AND customer_id = @customer_id AND (reversed_at IS NULL OR NOT slot_returned);

-- name: InsertRedemption :one
INSERT INTO coupon_redemptions (tenant_id, coupon_id, promotion_code_id, customer_id, target_kind, target_id, invoice_id, discount_minor, bonus_credits, currency)
VALUES (app_tenant_id(), @coupon_id, @promotion_code_id, @customer_id, @target_kind, @target_id, @invoice_id, @discount_minor, @bonus_credits, @currency)
RETURNING id;

-- name: GetLiveRedemption :one
SELECT r.id, r.coupon_id, c.code AS coupon_code, c.name AS coupon_name, r.promotion_code_id, COALESCE(p.code, '')::text AS promotion_code, r.discount_minor, r.bonus_credits,
  COALESCE(r.currency, '')::text AS currency
FROM coupon_redemptions r JOIN coupons c ON c.tenant_id = r.tenant_id AND c.id = r.coupon_id
LEFT JOIN promotion_codes p ON p.tenant_id = r.tenant_id AND p.id = r.promotion_code_id
WHERE r.tenant_id = app_tenant_id() AND r.target_kind = @target_kind AND r.target_id = @target_id AND r.reversed_at IS NULL;

-- name: LockLiveRedemption :one
-- The redemption of a purchase, with whether its code gives the place back, locked for the reversal.
SELECT r.id, r.coupon_id, r.promotion_code_id, COALESCE(p.restore_on_reversal, false)::boolean AS restore
FROM coupon_redemptions r LEFT JOIN promotion_codes p ON p.tenant_id = r.tenant_id AND p.id = r.promotion_code_id
WHERE r.tenant_id = app_tenant_id() AND r.target_kind = @target_kind AND r.target_id = @target_id AND r.reversed_at IS NULL
FOR UPDATE OF r;

-- name: GetSubscriptionRedemptionID :one
SELECT redemption_id FROM subscription_coupons WHERE tenant_id = app_tenant_id() AND subscription_id = $1;

-- name: LockRedemptionByID :one
SELECT r.id, r.coupon_id, r.promotion_code_id, COALESCE(p.restore_on_reversal, false)::boolean AS restore
FROM coupon_redemptions r LEFT JOIN promotion_codes p ON p.tenant_id = r.tenant_id AND p.id = r.promotion_code_id
WHERE r.tenant_id = app_tenant_id() AND r.id = $1 AND r.reversed_at IS NULL
FOR UPDATE OF r;

-- name: MarkRedemptionReversed :exec
UPDATE coupon_redemptions SET reversed_at = @now::timestamptz, slot_returned = @slot_returned::boolean WHERE tenant_id = app_tenant_id() AND id = @id;

-- name: SetRedemptionInvoice :exec
UPDATE coupon_redemptions SET invoice_id = @invoice_id WHERE tenant_id = app_tenant_id() AND id = @id AND invoice_id IS NULL;

-- name: ListCouponRedemptions :many
SELECT r.id, c.code AS coupon_code, COALESCE(p.code, '')::text AS promotion_code, COALESCE(cu.external_id, '')::text AS customer_external_id, r.target_kind, r.target_id,
  r.invoice_id, r.discount_minor, r.bonus_credits, COALESCE(r.currency, '')::text AS currency, r.created_at, r.reversed_at, count(*) OVER()::bigint AS _count
FROM coupon_redemptions r
JOIN coupons c ON c.tenant_id = r.tenant_id AND c.id = r.coupon_id
LEFT JOIN promotion_codes p ON p.tenant_id = r.tenant_id AND p.id = r.promotion_code_id
LEFT JOIN customers cu ON cu.tenant_id = r.tenant_id AND cu.id = r.customer_id
WHERE r.tenant_id = app_tenant_id()
  AND (sqlc.narg('coupon_code')::text IS NULL OR c.code = sqlc.narg('coupon_code'))
  AND (sqlc.narg('promotion_code')::text IS NULL OR upper(p.code) = upper(sqlc.narg('promotion_code')))
  AND (sqlc.narg('customer_external_id')::text IS NULL OR cu.external_id = sqlc.narg('customer_external_id'))
ORDER BY r.created_at DESC, r.id
LIMIT @row_limit OFFSET @row_offset;

-- name: InsertCustomerCoupon :execrows
INSERT INTO customer_coupons (tenant_id, customer_id, coupon_id, source, source_ref) VALUES (app_tenant_id(), @customer_id, @coupon_id, @source, @source_ref)
ON CONFLICT (tenant_id, customer_id, source, source_ref) DO NOTHING;

-- name: LockPendingCustomerCoupon :one
-- The oldest coupon waiting for the customer that still works, locked so two checkouts do not both use it.
SELECT cc.id, cc.coupon_id FROM customer_coupons cc JOIN coupons c ON c.tenant_id = cc.tenant_id AND c.id = cc.coupon_id
WHERE cc.tenant_id = app_tenant_id() AND cc.customer_id = @customer_id AND cc.consumed_at IS NULL AND c.active
  AND (c.expires_at IS NULL OR c.expires_at > @now::timestamptz) AND (c.max_redemptions IS NULL OR c.redeemed < c.max_redemptions)
ORDER BY cc.created_at, cc.id LIMIT 1
FOR UPDATE OF cc SKIP LOCKED;

-- name: ConsumeCustomerCoupon :exec
UPDATE customer_coupons SET consumed_at = @now::timestamptz, consumed_redemption_id = @redemption_id WHERE tenant_id = app_tenant_id() AND id = @id;
