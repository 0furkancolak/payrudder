-- The affiliate program (phase 47). A statement of the tenant side names the tenant through app_tenant_id() and RLS binds it as well. The statements marked
-- "system" run as plans_system with no tenant (the public link, a partner's own portal, the jobs) and name everything they touch.

-- name: GetAffiliateProgram :one
SELECT p.status, p.commission_kind, p.commission_value, p.currency, p.commission_term, p.commission_term_value, p.attribution_window_days, p.hold_days,
  p.min_payout_minor, p.referred_coupon_id, c.code AS referred_coupon_code, p.daily_click_cap, p.landing_url, p.auto_approve, p.created_at, p.updated_at
FROM affiliate_programs p LEFT JOIN coupons c ON c.tenant_id = p.tenant_id AND c.id = p.referred_coupon_id
WHERE p.tenant_id = app_tenant_id();

-- name: UpsertAffiliateProgram :exec
INSERT INTO affiliate_programs (tenant_id, status, commission_kind, commission_value, currency, commission_term, commission_term_value, attribution_window_days, hold_days,
  min_payout_minor, referred_coupon_id, daily_click_cap, landing_url, auto_approve)
VALUES (app_tenant_id(), @status, @commission_kind, @commission_value, @currency, @commission_term, @commission_term_value, @attribution_window_days, @hold_days,
  @min_payout_minor, @referred_coupon_id, @daily_click_cap, @landing_url, @auto_approve)
ON CONFLICT (tenant_id) DO UPDATE SET status = EXCLUDED.status, commission_kind = EXCLUDED.commission_kind, commission_value = EXCLUDED.commission_value,
  currency = EXCLUDED.currency, commission_term = EXCLUDED.commission_term, commission_term_value = EXCLUDED.commission_term_value,
  attribution_window_days = EXCLUDED.attribution_window_days, hold_days = EXCLUDED.hold_days, min_payout_minor = EXCLUDED.min_payout_minor,
  referred_coupon_id = EXCLUDED.referred_coupon_id, daily_click_cap = EXCLUDED.daily_click_cap, landing_url = EXCLUDED.landing_url,
  auto_approve = EXCLUDED.auto_approve, updated_at = now();

-- name: InsertAffiliate :one
INSERT INTO affiliates (tenant_id, code, email, name, status, payout_method, payout_details, source, approved_at)
VALUES (app_tenant_id(), @code, @email, @name, @status, @payout_method, @payout_details, @source, CASE WHEN @status::text = 'active' THEN now() END)
ON CONFLICT ((upper(code))) DO NOTHING
RETURNING id;

-- name: GetAffiliate :one
SELECT a.id, a.code, a.email, a.name, a.user_id, a.status, a.payout_method, a.payout_details, a.source, a.approved_at, a.created_at,
  (SELECT COALESCE(sum(c.clicks), 0) FROM affiliate_clicks_daily c WHERE c.tenant_id = a.tenant_id AND c.affiliate_id = a.id)::bigint AS clicks,
  (SELECT count(*) FROM affiliate_attributions t WHERE t.tenant_id = a.tenant_id AND t.affiliate_id = a.id)::bigint AS referrals
FROM affiliates a WHERE a.tenant_id = app_tenant_id() AND a.id = $1;

-- name: GetAffiliateByCode :one
SELECT a.id, a.code, a.email, a.name, a.user_id, a.status, a.payout_method, a.payout_details, a.source, a.approved_at, a.created_at,
  0::bigint AS clicks, 0::bigint AS referrals
FROM affiliates a WHERE a.tenant_id = app_tenant_id() AND upper(a.code) = upper(@code::text);

-- name: ListAffiliates :many
SELECT a.id, a.code, a.email, a.name, a.user_id, a.status, a.payout_method, a.payout_details, a.source, a.approved_at, a.created_at,
  (SELECT COALESCE(sum(c.clicks), 0) FROM affiliate_clicks_daily c WHERE c.tenant_id = a.tenant_id AND c.affiliate_id = a.id)::bigint AS clicks,
  (SELECT count(*) FROM affiliate_attributions t WHERE t.tenant_id = a.tenant_id AND t.affiliate_id = a.id)::bigint AS referrals,
  count(*) OVER()::bigint AS _count
FROM affiliates a
WHERE a.tenant_id = app_tenant_id()
  AND (sqlc.narg('status')::text IS NULL OR a.status = sqlc.narg('status'))
  AND (sqlc.narg('search')::text IS NULL OR a.code ILIKE '%' || sqlc.narg('search') || '%' OR a.email ILIKE '%' || sqlc.narg('search') || '%'
       OR a.name ILIKE '%' || sqlc.narg('search') || '%')
ORDER BY a.created_at DESC, a.id
LIMIT @row_limit OFFSET @row_offset;

-- name: UpdateAffiliate :execrows
UPDATE affiliates SET
  name = @name, payout_method = @payout_method, payout_details = @payout_details,
  status = @status,
  approved_at = CASE WHEN @status::text = 'active' AND approved_at IS NULL THEN now() ELSE approved_at END,
  suspended_at = CASE WHEN @status::text = 'suspended' THEN COALESCE(suspended_at, now()) ELSE NULL END,
  updated_at = now()
WHERE tenant_id = app_tenant_id() AND id = @id;

-- name: LinkPromotionCodeToAffiliate :execrows
UPDATE promotion_codes SET affiliate_id = @affiliate_id WHERE tenant_id = app_tenant_id() AND upper(code) = upper(@code::text);

-- name: GetPromotionCodeAffiliate :one
-- The affiliate a promotion code belongs to, if any (NULL when it belongs to none).
SELECT affiliate_id FROM promotion_codes WHERE tenant_id = app_tenant_id() AND upper(code) = upper(@code::text);

-- name: GetAttribution :one
SELECT affiliate_id, source, touched_at, attributed_at FROM affiliate_attributions WHERE tenant_id = app_tenant_id() AND customer_id = $1;

-- name: InsertAttribution :execrows
-- First touch wins: a customer who has an attribution keeps it, and the second insert changes nothing.
INSERT INTO affiliate_attributions (tenant_id, customer_id, affiliate_id, source, touched_at)
VALUES (app_tenant_id(), @customer_id, @affiliate_id, @source, @touched_at)
ON CONFLICT DO NOTHING;

-- name: InsertAttributionRejection :exec
INSERT INTO affiliate_attribution_rejections (tenant_id, affiliate_id, customer_id, reason)
VALUES (app_tenant_id(), @affiliate_id, @customer_id, @reason)
ON CONFLICT DO NOTHING;

-- name: GetCustomerForAttribution :one
SELECT id, external_id, email, payment_method FROM customers WHERE tenant_id = app_tenant_id() AND id = $1;

-- name: GetCustomerForAttributionByExternalID :one
SELECT id, external_id, email, payment_method FROM customers WHERE tenant_id = app_tenant_id() AND external_id = $1;

-- name: PaymentMethodsOfEmail :many
-- The payment methods of the customers who have this e-mail address (the affiliate's own, if they are also a customer).
SELECT payment_method FROM customers
WHERE tenant_id = app_tenant_id() AND lower(email) = lower(@email::text) AND payment_method IS NOT NULL AND payment_method <> '';

-- name: ListAffiliateReferrals :many
SELECT t.affiliate_id, a.code AS affiliate_code, c.external_id AS customer_external_id, c.email AS customer_email, t.source, t.attributed_at,
  count(*) OVER()::bigint AS _count
FROM affiliate_attributions t
JOIN affiliates a ON a.tenant_id = t.tenant_id AND a.id = t.affiliate_id
JOIN customers c ON c.tenant_id = t.tenant_id AND c.id = t.customer_id
WHERE t.tenant_id = app_tenant_id() AND (sqlc.narg('affiliate_id')::uuid IS NULL OR t.affiliate_id = sqlc.narg('affiliate_id'))
ORDER BY t.attributed_at DESC, t.customer_id
LIMIT @row_limit OFFSET @row_offset;

-- name: SystemResolveAffiliateCode :one
-- system. The platform-wide code finds its tenant, affiliate and program for the public link.
SELECT a.tenant_id, a.id, a.code, a.status AS affiliate_status, p.status AS program_status, p.attribution_window_days, p.daily_click_cap, p.landing_url
FROM affiliates a JOIN affiliate_programs p ON p.tenant_id = a.tenant_id
WHERE upper(a.code) = upper(@code::text);

-- name: SystemCountClick :execrows
-- system. One more visit today, unless the affiliate's daily cap is reached (no row is touched then).
INSERT INTO affiliate_clicks_daily AS d (tenant_id, affiliate_id, day, clicks) VALUES (@tenant_id, @affiliate_id, @day, 1)
ON CONFLICT (tenant_id, affiliate_id, day) DO UPDATE SET clicks = d.clicks + 1 WHERE d.clicks < @cap::int;
