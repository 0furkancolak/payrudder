-- Runtime health of provider accounts and failover (docs/plans/22 G8). Every statement names the tenant.

-- name: PutAccountCheck :exec
-- A periodic check: what it found and when. The breaker columns are the breaker's and are not touched.
INSERT INTO provider_account_health (tenant_id, provider, environment, status, last_error, last_checked_at, last_ok_at, last_failure_at)
VALUES (@tenant_id, @provider, @environment, @status, @last_error, @checked_at, sqlc.narg('last_ok_at'), sqlc.narg('last_failure_at'))
ON CONFLICT (tenant_id, provider, environment) DO UPDATE SET
  status = EXCLUDED.status, last_error = EXCLUDED.last_error, last_checked_at = EXCLUDED.last_checked_at,
  last_ok_at = COALESCE(EXCLUDED.last_ok_at, provider_account_health.last_ok_at),
  last_failure_at = COALESCE(EXCLUDED.last_failure_at, provider_account_health.last_failure_at), updated_at = now();

-- name: PutAccountBreaker :exec
-- The breaker changed state. The status follows it only when the account is down or back: a breaker that closed does not say the credentials are good.
INSERT INTO provider_account_health (tenant_id, provider, environment, status, breaker, consecutive_failures, last_error, opened_at, last_failure_at)
VALUES (@tenant_id, @provider, @environment, @status, @breaker, @failures, @last_error, sqlc.narg('opened_at'), sqlc.narg('last_failure_at'))
ON CONFLICT (tenant_id, provider, environment) DO UPDATE SET
  breaker = EXCLUDED.breaker, consecutive_failures = EXCLUDED.consecutive_failures,
  status = CASE WHEN EXCLUDED.breaker = 'closed' AND provider_account_health.status = 'down' THEN 'healthy'
                WHEN EXCLUDED.breaker <> 'closed' THEN 'down' ELSE provider_account_health.status END,
  last_error = CASE WHEN EXCLUDED.breaker <> 'closed' THEN EXCLUDED.last_error ELSE provider_account_health.last_error END,
  opened_at = COALESCE(EXCLUDED.opened_at, provider_account_health.opened_at),
  last_failure_at = COALESCE(EXCLUDED.last_failure_at, provider_account_health.last_failure_at), updated_at = now();

-- name: ListAccountHealth :many
-- Every account of the tenant with what is known of its health and where its charges would fail over to.
SELECT a.provider, a.environment, a.is_default,
  COALESCE(h.status, 'unknown')::text AS status, COALESCE(h.breaker, 'closed')::text AS breaker, COALESCE(h.consecutive_failures, 0)::bigint AS consecutive_failures,
  COALESCE(h.last_error, '')::text AS last_error, h.last_checked_at, h.last_ok_at, h.last_failure_at, h.opened_at, COALESCE(f.failover_to, '')::text AS failover_to
FROM provider_accounts a
LEFT JOIN provider_account_health h ON h.tenant_id = a.tenant_id AND h.provider = a.provider AND h.environment = a.environment
LEFT JOIN provider_failover f ON f.tenant_id = a.tenant_id AND f.provider = a.provider AND f.environment = a.environment
WHERE a.tenant_id = @tenant_id ORDER BY a.environment, a.provider;

-- name: ListAllAccounts :many
-- The accounts of every tenant of this database (of the pooled one, or of the one tenant a cell schema holds).
SELECT tenant_id, provider, environment FROM provider_accounts ORDER BY tenant_id, environment, provider;

-- name: GetFailover :one
SELECT failover_to FROM provider_failover WHERE tenant_id = @tenant_id AND environment = @environment AND provider = @provider;

-- name: PutFailover :exec
INSERT INTO provider_failover (tenant_id, environment, provider, failover_to, updated_by) VALUES (@tenant_id, @environment, @provider, @failover_to, @updated_by)
ON CONFLICT (tenant_id, environment, provider) DO UPDATE SET failover_to = EXCLUDED.failover_to, updated_by = EXCLUDED.updated_by, updated_at = now();

-- name: DeleteFailover :execrows
DELETE FROM provider_failover WHERE tenant_id = @tenant_id AND environment = @environment AND provider = @provider;

-- name: GetFailoverAttempt :one
-- The attempt a charge made at another account after the account it was routed to definitely did not process it: its key starts with the charge's key.
SELECT provider, idempotency_key FROM payment_attempts
WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id AND kind = 'charge' AND starts_with(idempotency_key, @prefix::text) ORDER BY created_at DESC LIMIT 1;
