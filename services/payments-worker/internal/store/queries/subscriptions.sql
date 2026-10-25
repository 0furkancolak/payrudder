-- name: UpsertProviderSubscription :exec
INSERT INTO provider_subscriptions (tenant_id, provider, environment, provider_ref, quotify_subscription_id, status, next_billed_at, updated_at)
VALUES (@tenant_id, @provider, @environment, @provider_ref, @quotify_subscription_id, @status, @next_billed_at, @updated_at)
ON CONFLICT (tenant_id, provider, environment, provider_ref) DO UPDATE
SET status = EXCLUDED.status, next_billed_at = EXCLUDED.next_billed_at,
    quotify_subscription_id = COALESCE(EXCLUDED.quotify_subscription_id, provider_subscriptions.quotify_subscription_id), updated_at = EXCLUDED.updated_at
WHERE provider_subscriptions.updated_at <= EXCLUDED.updated_at;

-- name: ListProviderSubscriptions :many
SELECT provider, environment, provider_ref, quotify_subscription_id, status, next_billed_at, updated_at
FROM provider_subscriptions WHERE tenant_id = @tenant_id ORDER BY updated_at DESC LIMIT 500;

-- name: GetProviderSubscriptionPayrudderID :one
SELECT quotify_subscription_id FROM provider_subscriptions
WHERE tenant_id = @tenant_id AND provider = @provider AND environment = @environment AND provider_ref = @provider_ref;

-- name: GetProviderSubscriptionNextBilledAt :one
SELECT next_billed_at FROM provider_subscriptions
WHERE tenant_id = @tenant_id AND provider = @provider AND environment = @environment AND provider_ref = @provider_ref;
