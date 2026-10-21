-- name: SubscriptionReport :many
-- Counts and recurring revenue of a month, split by currency and optionally by provider and country. A dimension that is not split reports ''.
-- month_start/month_end bound the month [start, end); month_last is its last instant.
WITH subs AS (
  SELECT s.id, s.currency, lower(s.active) AS starts, upper(s.active) AS ends, s.changed_from_id,
         COALESCE(s.provider, '') AS provider, COALESCE(c.country, '') AS country, pl.code AS plan_code,
         CASE WHEN s.state = 'paused' THEN 'paused'
              WHEN s.state = 'past_due' THEN 'past_due'
              WHEN s.state = 'trialing' AND s.trial_ends_at > sqlc.arg('month_last')::timestamptz THEN 'trialing'
              ELSE 'active' END AS eff,
         CASE WHEN s.interval = 'year' THEN ROUND(COALESCE(f.base_fee_minor, 0) / 12.0)::bigint
              WHEN s.currency = sqlc.arg('tenant_currency')::text THEN pv.base_fee_minor ELSE COALESCE(f.base_fee_minor, 0) END AS fee,
         EXISTS (SELECT 1 FROM subscriptions n WHERE n.tenant_id = s.tenant_id AND n.changed_from_id = s.id) AS has_successor
  FROM subscriptions s
  JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id
  JOIN plan_versions pv ON pv.tenant_id = s.tenant_id AND pv.id = s.plan_version_id
  JOIN plans pl ON pl.tenant_id = pv.tenant_id AND pl.id = pv.plan_id
  LEFT JOIN plan_version_fees f ON f.tenant_id = s.tenant_id AND f.plan_version_id = s.plan_version_id AND f.currency = s.currency AND f.interval = s.interval
  WHERE s.tenant_id = app_tenant_id()
), flagged AS (
  SELECT *,
         (starts <= sqlc.arg('month_last')::timestamptz AND (ends IS NULL OR ends > sqlc.arg('month_last')::timestamptz)) AS alive,
         (starts >= sqlc.arg('month_start')::timestamptz AND starts < sqlc.arg('month_end')::timestamptz AND changed_from_id IS NULL) AS is_new,
         (ends IS NOT NULL AND ends >= sqlc.arg('month_start')::timestamptz AND ends < sqlc.arg('month_end')::timestamptz AND NOT has_successor) AS is_churned
  FROM subs
), split AS (
  SELECT CASE WHEN sqlc.arg('by_provider')::bool THEN provider ELSE '' END AS provider,
         CASE WHEN sqlc.arg('by_country')::bool THEN country ELSE '' END AS country,
         CASE WHEN sqlc.arg('by_plan')::bool THEN plan_code ELSE '' END AS plan_code,
         currency, alive, is_new, is_churned, eff, fee
  FROM flagged
  WHERE alive OR is_new OR is_churned
)
SELECT provider::text AS provider, country::text AS country, plan_code::text AS plan_code, currency,
  (count(*) FILTER (WHERE alive AND eff = 'active'))::bigint AS active,
  (count(*) FILTER (WHERE alive AND eff = 'trialing'))::bigint AS trialing,
  (count(*) FILTER (WHERE alive AND eff = 'paused'))::bigint AS paused,
  (count(*) FILTER (WHERE alive AND eff = 'past_due'))::bigint AS past_due,
  COALESCE(sum(fee) FILTER (WHERE alive AND eff IN ('active', 'past_due')), 0)::bigint AS mrr_minor,
  (count(*) FILTER (WHERE is_new))::bigint AS new_subscriptions,
  COALESCE(sum(fee) FILTER (WHERE is_new AND eff IN ('active', 'past_due')), 0)::bigint AS new_mrr_minor,
  (count(*) FILTER (WHERE is_churned))::bigint AS churned,
  COALESCE(sum(fee) FILTER (WHERE is_churned), 0)::bigint AS churned_mrr_minor
FROM split
GROUP BY provider, country, plan_code, currency
ORDER BY provider, country, plan_code, currency;

-- name: CustomerStats :one
-- How many customers the tenant has, and how many were created in two ranges (a period and the one before it, for a comparison). A range with equal bounds is empty.
SELECT count(*)::bigint AS total,
       (count(*) FILTER (WHERE created_at >= sqlc.arg('from_at')::timestamptz AND created_at < sqlc.arg('to_at')::timestamptz))::bigint AS new_in_range,
       (count(*) FILTER (WHERE created_at >= sqlc.arg('prev_from_at')::timestamptz AND created_at < sqlc.arg('prev_to_at')::timestamptz))::bigint AS new_in_previous
FROM customers
WHERE tenant_id = app_tenant_id();
