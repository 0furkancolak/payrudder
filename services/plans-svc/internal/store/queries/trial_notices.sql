-- name: SystemListTrialsEndingSoon :many
-- system. The trialing subscriptions Payrudder runs whose trial ends in (from, until] and whose trial end has no notice yet, oldest end first. Across tenants;
-- the job re-checks each one in its tenant's transaction before it records the notice.
SELECT s.tenant_id, s.id, s.customer_id, s.trial_ends_at::timestamptz AS trial_ends_at
FROM subscriptions s
WHERE s.state = 'trialing' AND upper_inf(s.active) AND NOT s.cancel_at_period_end AND s.owner = @owner::text
  AND s.trial_ends_at > @from_time::timestamptz AND s.trial_ends_at <= @until::timestamptz
  AND NOT EXISTS (SELECT 1 FROM subscription_trial_notices n
                  WHERE n.tenant_id = s.tenant_id AND n.customer_id = s.customer_id AND n.trial_ends_at = s.trial_ends_at)
ORDER BY s.trial_ends_at, s.id
LIMIT @batch::int;

-- name: InsertTrialNotice :execrows
-- Records that the customer was reminded of this end of a trial. 0 rows: it was already (another pass, another instance, the subscription this one replaced).
INSERT INTO subscription_trial_notices (tenant_id, customer_id, trial_ends_at, subscription_id, notified_at)
VALUES (app_tenant_id(), @customer_id, @trial_ends_at, @subscription_id, @notified_at)
ON CONFLICT (tenant_id, customer_id, trial_ends_at) DO NOTHING;
