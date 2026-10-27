-- Resetting a sandbox (phase 45 E). Run as plans_system from internal/sandbox only; every statement names the tenant.

-- The customers' side of the tenant, children first. The configuration (meters, plans, prices, tax, coupons and promotion codes, routing, mail, webhook endpoints, keys)
-- is not here.
-- name: ResetCustomerCoupons :execrows
DELETE FROM customer_coupons WHERE tenant_id = $1;
-- name: ResetCouponRedemptions :execrows
DELETE FROM coupon_redemptions WHERE tenant_id = $1;
-- The partner program's customer side: commissions, their reversals, who was attributed and the clicks. The program and the partners are configuration and stay.
-- name: ResetAffiliateReversals :execrows
DELETE FROM affiliate_reversals WHERE tenant_id = $1;
-- name: ResetAffiliateCommissions :execrows
DELETE FROM affiliate_commissions WHERE tenant_id = $1;
-- The payouts made to partners and their postings: they describe commissions that are gone. The journal first, it refers to the payouts.
-- name: ResetAffiliateJournal :execrows
DELETE FROM affiliate_journal WHERE tenant_id = $1;
-- name: ResetAffiliatePayouts :execrows
DELETE FROM affiliate_payouts WHERE tenant_id = $1;
-- name: ResetAffiliateAttributions :execrows
DELETE FROM affiliate_attributions WHERE tenant_id = $1;
-- name: ResetAffiliateAttributionRejections :execrows
DELETE FROM affiliate_attribution_rejections WHERE tenant_id = $1;
-- name: ResetAffiliateClicks :execrows
DELETE FROM affiliate_clicks_daily WHERE tenant_id = $1;
-- name: ResetSubscriptionAdjustments :execrows
DELETE FROM subscription_adjustments WHERE tenant_id = $1;
-- name: ResetSubscriptionChanges :execrows
DELETE FROM subscription_changes WHERE tenant_id = $1;
-- name: ResetSubscriptionCoupons :execrows
DELETE FROM subscription_coupons WHERE tenant_id = $1;
-- name: ResetProviderMigrations :execrows
DELETE FROM provider_migrations WHERE tenant_id = $1;
-- name: ResetSubscriptions :execrows
DELETE FROM subscriptions WHERE tenant_id = $1;
-- name: ResetCustomers :execrows
DELETE FROM customers WHERE tenant_id = $1;
-- name: ResetWebhookDeliveries :execrows
DELETE FROM webhook_deliveries WHERE tenant_id = $1;
-- name: ResetWebhookEvents :execrows
DELETE FROM webhook_events WHERE tenant_id = $1;
-- name: ResetEntitlementOverrides :execrows
DELETE FROM customer_entitlement_overrides WHERE tenant_id = $1;
-- name: ResetEntitlementOutbox :execrows
DELETE FROM outbox WHERE tenant_id = $1;
-- name: ResetCouponCounters :exec
UPDATE coupons SET redeemed = 0 WHERE tenant_id = $1 AND redeemed <> 0;
-- name: ResetPromotionCodeCounters :exec
UPDATE promotion_codes SET redeemed = 0 WHERE tenant_id = $1 AND redeemed <> 0;

-- The job.

-- name: InsertSandboxReset :one
INSERT INTO sandbox_resets (id, app_id, tenant_id, requested_by, kind, steps) VALUES (@id, @app_id, @tenant_id, @requested_by, @kind, @steps) RETURNING *;

-- name: LatestSandboxReset :one
SELECT * FROM sandbox_resets WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT 1;

-- A manual reset is at most one every few minutes per sandbox; a retention reset does not count against a person.
-- name: CountRecentManualResets :one
SELECT count(*) FROM sandbox_resets WHERE tenant_id = @tenant_id AND kind = 'manual' AND created_at > now() - @within::interval;

-- One job to run: a queued one, or one whose runner stopped answering. FOR UPDATE SKIP LOCKED lets several replicas poll without taking the same job.
-- name: ClaimSandboxReset :one
UPDATE sandbox_resets SET state = 'running', started_at = COALESCE(started_at, now()), lease_until = now() + @lease::interval
WHERE id = (
  SELECT r.id FROM sandbox_resets r
  WHERE r.state = 'queued' OR (r.state = 'running' AND r.lease_until < now())
  ORDER BY r.created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
RETURNING *;

-- name: SaveSandboxResetSteps :exec
UPDATE sandbox_resets SET steps = @steps, lease_until = now() + @lease::interval WHERE id = @id AND state = 'running';

-- name: FinishSandboxReset :exec
UPDATE sandbox_resets SET state = @state, error = @error, steps = @steps, finished_at = now(), lease_until = NULL WHERE id = @id;

-- The sandboxes nobody has touched for @idle: no key use, no customer or subscription made, no reset, since then; none with a reset open. The retention sweep resets them.
-- name: IdleSandboxTenants :many
SELECT t.id AS tenant_id, t.app_id
FROM tenants t
WHERE t.environment = 'sandbox'
  -- The platform's own tenant (phase 55) holds the plans and the customers of the SaaS: it is never idle.
  AND NOT EXISTS (SELECT 1 FROM accounts a WHERE a.id = t.account_id AND a.platform)
  AND GREATEST(
        t.created_at,
        COALESCE((SELECT max(k.last_used_at) FROM api_keys k WHERE k.tenant_id = t.id), t.created_at),
        COALESCE((SELECT max(c.created_at) FROM customers c WHERE c.tenant_id = t.id), t.created_at),
        COALESCE((SELECT max(s.created_at) FROM subscriptions s WHERE s.tenant_id = t.id), t.created_at),
        COALESCE((SELECT max(r.finished_at) FROM sandbox_resets r WHERE r.tenant_id = t.id), t.created_at)
      ) < now() - @idle::interval
  AND NOT EXISTS (SELECT 1 FROM sandbox_resets r WHERE r.tenant_id = t.id AND r.state IN ('queued', 'running'))
ORDER BY t.created_at
LIMIT @batch::int;
