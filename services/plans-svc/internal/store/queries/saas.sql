-- Payrudder as a SaaS (phase 55). Run as plans_system from internal/saas only. The platform tenant's rows are read by naming its tenant_id in every statement
-- (the system role bypasses RLS, so the predicate is the isolation); writes to the platform tenant go through the ordinary PlansService handlers in a tenant
-- transaction of the app pool.

-- Tried, not waited for: a replica that waits would hold one of the pool's few connections while the one that has the lock needs more of them.
-- name: SaasTryLockBootstrap :one
SELECT pg_try_advisory_xact_lock(hashtextextended('payrudder-saas-bootstrap', 0))::bool;

-- name: SaasGetPlatformAccount :one
SELECT * FROM accounts WHERE platform;

-- name: SaasInsertPlatformAccount :one
INSERT INTO accounts (id, name, platform) VALUES ($1, $2, true) RETURNING *;

-- name: SaasGetPlatformTenant :one
SELECT t.* FROM tenants t JOIN accounts a ON a.id = t.account_id WHERE a.platform ORDER BY t.environment LIMIT 1;

-- name: SaasTenantAccount :one
SELECT t.account_id, t.environment, a.platform FROM tenants t JOIN accounts a ON a.id = t.account_id WHERE t.id = $1;

-- name: SaasGetAccount :one
SELECT * FROM accounts WHERE id = $1;

-- Usage of an account: what the plan's limits are counted against.
-- name: SaasCountApps :one
SELECT count(*) FROM apps WHERE account_id = $1;

-- name: SaasCountMembers :one
SELECT count(*) FROM account_members WHERE account_id = $1 AND role <> 'affiliate';

-- name: SaasCountOpenInvites :one
SELECT count(*) FROM account_invites WHERE account_id = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now();

-- name: SaasCountActiveKeys :one
SELECT count(*) FROM api_keys k JOIN tenants t ON t.id = k.tenant_id
WHERE t.account_id = $1 AND k.status = 'active' AND (k.expires_at IS NULL OR k.expires_at > now());

-- name: SaasOwnerEmail :one
SELECT u.email FROM account_members m JOIN users u ON u.id = m.user_id WHERE m.account_id = $1 AND m.role = 'owner' ORDER BY m.created_at, u.id LIMIT 1;

-- The organisation as a customer of the platform tenant.
-- name: SaasGetCustomer :one
SELECT id, external_id, name FROM customers WHERE tenant_id = @tenant_id AND external_id = @account_id::text;

-- The subscription in force: not ended, not cancelled; the newest start wins if a bug ever left two.
-- name: SaasCurrentSubscription :one
SELECT s.id, s.customer_id, s.plan_version_id, s.state, s.trial_ends_at, s.cancel_at_period_end, s.currency,
       lower(s.active)::timestamptz AS start_time, upper_inf(s.active)::bool AS open_ended, COALESCE(upper(s.active), lower(s.active))::timestamptz AS end_time,
       v.version AS plan_version, v.base_fee_minor, p.code AS plan_code, p.name AS plan_name
FROM customers c
JOIN subscriptions s ON s.tenant_id = c.tenant_id AND s.customer_id = c.id
JOIN plan_versions v ON v.tenant_id = s.tenant_id AND v.id = s.plan_version_id
JOIN plans p ON p.tenant_id = v.tenant_id AND p.id = v.plan_id
WHERE c.tenant_id = @tenant_id AND c.external_id = @account_id::text AND s.state <> 'cancelled' AND s.active @> now()
ORDER BY lower(s.active) DESC, s.id DESC
LIMIT 1;

-- name: SaasHadTrial :one
SELECT EXISTS (
  SELECT 1 FROM customers c JOIN subscriptions s ON s.tenant_id = c.tenant_id AND s.customer_id = c.id
  WHERE c.tenant_id = @tenant_id AND c.external_id = @account_id::text AND s.trial_ends_at IS NOT NULL
)::bool;

-- The latest version of every plan of the platform tenant.
-- name: SaasLatestPlanVersions :many
SELECT DISTINCT ON (p.id) v.id, v.version, v.base_fee_minor, p.code AS plan_code, p.name AS plan_name
FROM plan_versions v JOIN plans p ON p.tenant_id = v.tenant_id AND p.id = v.plan_id
WHERE v.tenant_id = @tenant_id
ORDER BY p.id, v.version DESC;

-- name: SaasEntitlements :many
SELECT meter_code, limit_units, mode FROM plan_entitlements WHERE tenant_id = @tenant_id AND plan_version_id = @plan_version_id;

-- name: SaasOverrides :many
SELECT o.meter_code, o.limit_units, o.mode
FROM customer_entitlement_overrides o JOIN customers c ON c.tenant_id = o.tenant_id AND c.id = o.customer_id
WHERE o.tenant_id = @tenant_id AND c.external_id = @account_id::text;

-- Accounts that are not customers yet, or whose customer has no subscription in force (a cancelled one falls back to Free).
-- name: SaasAccountsToEnsure :many
SELECT a.id FROM accounts a
WHERE NOT a.platform AND NOT EXISTS (
  SELECT 1 FROM customers c JOIN subscriptions s ON s.tenant_id = c.tenant_id AND s.customer_id = c.id
  WHERE c.tenant_id = @tenant_id AND c.external_id = a.id::text AND s.state <> 'cancelled' AND s.active @> now()
)
ORDER BY a.created_at, a.id
LIMIT @batch::int;

-- The operator's list: every organisation but the platform's own, with its owner and its usage.
-- name: SaasListOrganizations :many
SELECT a.id, a.name, a.created_at,
       COALESCE((SELECT u.email FROM account_members m JOIN users u ON u.id = m.user_id WHERE m.account_id = a.id AND m.role = 'owner' ORDER BY m.created_at, u.id LIMIT 1), '')::text AS owner_email,
       (SELECT count(*) FROM apps ap WHERE ap.account_id = a.id)::bigint AS apps,
       (SELECT count(*) FROM account_members m WHERE m.account_id = a.id AND m.role <> 'affiliate')::bigint AS members,
       (SELECT count(*) FROM api_keys k JOIN tenants t ON t.id = k.tenant_id WHERE t.account_id = a.id AND k.status = 'active' AND (k.expires_at IS NULL OR k.expires_at > now()))::bigint AS api_keys
FROM accounts a
WHERE NOT a.platform AND a.id > @after::uuid
  AND (@query::text = '' OR a.name ILIKE '%' || @query::text || '%'
       OR EXISTS (SELECT 1 FROM account_members m JOIN users u ON u.id = m.user_id WHERE m.account_id = a.id AND u.email ILIKE '%' || @query::text || '%'))
ORDER BY a.id
LIMIT @page_size::int;
