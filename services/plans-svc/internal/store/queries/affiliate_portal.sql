-- The partner's own view (phase 47). Run as plans_system from affiliates/portal only. A partner is a person, not a tenant, so nothing here names the tenant through
-- app_tenant_id(): every statement finds its rows through the person (affiliates.user_id), and the figures are read afterwards through the tenant's own queries.

-- name: PortalListAffiliations :many
SELECT a.id, a.tenant_id, t.environment, COALESCE(ap.name, t.name) AS seller_name, a.code, a.status, a.name, a.email, a.payout_method, a.payout_details, a.created_at,
  COALESCE(p.status, '') AS program_status, COALESCE(p.commission_kind, '') AS commission_kind, COALESCE(p.commission_value, 0)::bigint AS commission_value,
  COALESCE(p.currency, '')::text AS program_currency, COALESCE(p.commission_term, '') AS commission_term, COALESCE(p.commission_term_value, 0)::int AS commission_term_value,
  COALESCE(p.attribution_window_days, 0)::int AS attribution_window_days, COALESCE(p.hold_days, 0)::int AS hold_days, COALESCE(p.min_payout_minor, 0)::bigint AS min_payout_minor
FROM affiliates a
JOIN tenants t ON t.id = a.tenant_id
LEFT JOIN apps ap ON ap.id = t.app_id
LEFT JOIN affiliate_programs p ON p.tenant_id = a.tenant_id
WHERE a.user_id = @user_id AND (sqlc.narg('affiliate_id')::uuid IS NULL OR a.id = sqlc.narg('affiliate_id'))
ORDER BY t.environment DESC, a.created_at DESC, a.id;

-- name: PortalUser :one
SELECT email, email_verified_at FROM users WHERE id = $1;

-- name: PortalClaimAffiliates :many
-- The partner records a seller made for this address, not claimed by anyone yet.
UPDATE affiliates SET user_id = @user_id, updated_at = now() WHERE user_id IS NULL AND lower(email) = lower(@email::text) RETURNING id, tenant_id;

-- name: PortalTenantAccount :one
SELECT account_id FROM tenants WHERE id = $1;

-- name: PortalAddAffiliateMember :exec
-- The person is a member of the seller's account with the role affiliate, which sees none of the account (an owner who is also a partner keeps their role).
INSERT INTO account_members (account_id, user_id, role) VALUES (@account_id, @user_id, 'affiliate') ON CONFLICT DO NOTHING;

-- name: PortalProgram :one
-- A seller's program for someone who is about to apply, and whether they have applied already.
SELECT t.id AS tenant_id, t.environment, t.status AS tenant_status, COALESCE(ap.name, t.name) AS seller_name, p.status, p.commission_kind, p.commission_value, p.currency,
  p.commission_term, p.commission_term_value, p.attribution_window_days, p.hold_days, p.min_payout_minor, p.auto_approve,
  EXISTS (SELECT 1 FROM affiliates a WHERE a.tenant_id = t.id AND a.user_id = @user_id) AS applied
FROM affiliate_programs p JOIN tenants t ON t.id = p.tenant_id LEFT JOIN apps ap ON ap.id = t.app_id
WHERE p.tenant_id = @tenant_id;

-- name: PortalInsertAffiliate :one
INSERT INTO affiliates (tenant_id, code, email, name, user_id, status, payout_method, payout_details, source, approved_at)
VALUES (@tenant_id, @code, @email, @name, @user_id, @status, @payout_method, @payout_details, 'application', CASE WHEN @status::text = 'active' THEN now() END)
ON CONFLICT ((upper(code))) DO NOTHING
RETURNING id;

-- name: PortalUpdateAffiliation :execrows
UPDATE affiliates SET name = @name, payout_method = @payout_method, payout_details = @payout_details, updated_at = now() WHERE id = @id AND user_id = @user_id;

-- A tenant in a cell (ADR 0018) has its affiliates and program in its schema and nothing else the statements above join: the pooled side (the tenant's environment, status
-- and the seller's name) is PortalTenantInfo, the tenant's side the statements below, and the partner portal puts the two together.

-- name: PortalTenantInfo :one
SELECT t.id AS tenant_id, t.environment, t.status AS tenant_status, COALESCE(ap.name, t.name) AS seller_name
FROM tenants t LEFT JOIN apps ap ON ap.id = t.app_id
WHERE t.id = $1;

-- name: PortalListAffiliationsInTenant :many
SELECT a.id, a.tenant_id, a.code, a.status, a.name, a.email, a.payout_method, a.payout_details, a.created_at,
  COALESCE(p.status, '') AS program_status, COALESCE(p.commission_kind, '') AS commission_kind, COALESCE(p.commission_value, 0)::bigint AS commission_value,
  COALESCE(p.currency, '')::text AS program_currency, COALESCE(p.commission_term, '') AS commission_term, COALESCE(p.commission_term_value, 0)::int AS commission_term_value,
  COALESCE(p.attribution_window_days, 0)::int AS attribution_window_days, COALESCE(p.hold_days, 0)::int AS hold_days, COALESCE(p.min_payout_minor, 0)::bigint AS min_payout_minor
FROM affiliates a
LEFT JOIN affiliate_programs p ON p.tenant_id = a.tenant_id
WHERE a.user_id = @user_id AND (sqlc.narg('affiliate_id')::uuid IS NULL OR a.id = sqlc.narg('affiliate_id'))
ORDER BY a.created_at DESC, a.id;

-- name: PortalProgramInTenant :one
SELECT p.tenant_id, p.status, p.commission_kind, p.commission_value, p.currency, p.commission_term, p.commission_term_value, p.attribution_window_days, p.hold_days,
  p.min_payout_minor, p.auto_approve,
  EXISTS (SELECT 1 FROM affiliates a WHERE a.tenant_id = p.tenant_id AND a.user_id = @user_id) AS applied
FROM affiliate_programs p
WHERE p.tenant_id = @tenant_id;

-- name: PortalFindUnclaimed :many
-- The partner records a seller made for this address, not claimed by anyone yet (PortalClaimAffiliates takes them).
SELECT id, tenant_id FROM affiliates WHERE user_id IS NULL AND lower(email) = lower(@email::text);
