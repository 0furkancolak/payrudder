-- The go-live gate (phase 45). Run as plans_system from internal/golive only; nothing here is tenant data a request could name.

-- Serialises two activations of one tenant and reads the row they decide on.
-- name: LockTenantForGoLive :one
SELECT * FROM tenants WHERE id = $1 FOR UPDATE;

-- True when a member who owns the account has proven an address.
-- name: AccountOwnerVerified :one
SELECT EXISTS (
  SELECT 1 FROM account_members m JOIN users u ON u.id = m.user_id
  WHERE m.account_id = $1 AND m.role = 'owner' AND u.email_verified_at IS NOT NULL
) AS verified;

-- True when the tenant has published at least one plan version.
-- name: TenantHasPublishedPlan :one
SELECT EXISTS (SELECT 1 FROM plan_versions WHERE tenant_id = $1) AS published;

-- The first acceptance is the one kept.
-- name: AcceptTenantTerms :exec
UPDATE tenants SET terms_accepted_at = COALESCE(terms_accepted_at, now()), terms_accepted_by = COALESCE(terms_accepted_by, @user_id)
WHERE id = @id;
