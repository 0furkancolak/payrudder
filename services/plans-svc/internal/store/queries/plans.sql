-- Tenant-scoped queries, run as plans_app inside pgxx.InTenantTx. The explicit tenant_id = app_tenant_id() predicates
-- duplicate what RLS enforces anyway; they keep the (tenant_id, …) indexes obvious to the planner and to readers.

-- name: CurrentTenant :one
SELECT id, environment, tier, currency, timezone FROM current_tenant();

-- name: CreateCustomer :one
INSERT INTO customers (tenant_id, id, external_id, name, email, payment_method, country, billing_currency)
VALUES (app_tenant_id(), $1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetCustomerByExternalID :one
SELECT * FROM customers WHERE tenant_id = app_tenant_id() AND external_id = $1;

-- name: GetCustomerByID :one
SELECT * FROM customers WHERE tenant_id = app_tenant_id() AND id = $1;

-- name: ListCustomers :many
SELECT * FROM customers
WHERE tenant_id = app_tenant_id() AND external_id > @after::text
ORDER BY external_id
LIMIT @page_size;

-- name: CountCustomers :one
SELECT count(*) FROM customers WHERE tenant_id = app_tenant_id();

-- name: SetPaymentMethod :one
UPDATE customers SET payment_method = $2
WHERE tenant_id = app_tenant_id() AND external_id = $1
RETURNING *;

-- name: LatestMeter :one
SELECT * FROM meters WHERE tenant_id = app_tenant_id() AND code = $1 ORDER BY version DESC LIMIT 1;

-- name: InsertMeter :one
INSERT INTO meters (tenant_id, code, version, aggregation, value_property, filter, quantity_scale, unit_label)
VALUES (app_tenant_id(), $1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListLatestMeters :many
SELECT DISTINCT ON (code) * FROM meters WHERE tenant_id = app_tenant_id() ORDER BY code, version DESC;

-- name: CountMeterCodes :one
SELECT count(DISTINCT code) FROM meters WHERE tenant_id = app_tenant_id();

-- name: InsertPlan :one
INSERT INTO plans (tenant_id, id, code, name) VALUES (app_tenant_id(), $1, $2, $3) RETURNING *;

-- name: GetPlanByCode :one
SELECT p.id, p.code, p.name, p.created_at,
       COALESCE((SELECT max(v.version) FROM plan_versions v WHERE v.tenant_id = p.tenant_id AND v.plan_id = p.id), 0)::int AS latest_version
FROM plans p WHERE p.tenant_id = app_tenant_id() AND p.code = $1;

-- name: ListPlans :many
SELECT p.id, p.code, p.name, p.created_at,
       COALESCE((SELECT max(v.version) FROM plan_versions v WHERE v.tenant_id = p.tenant_id AND v.plan_id = p.id), 0)::int AS latest_version
FROM plans p WHERE p.tenant_id = app_tenant_id() ORDER BY p.code;

-- name: InsertPlanVersion :one
-- Next version number; a concurrent publish loses on UNIQUE (tenant_id, plan_id, version) and is retried.
INSERT INTO plan_versions (tenant_id, id, plan_id, version, base_fee_minor)
VALUES (app_tenant_id(), @id, @plan_id,
        (SELECT COALESCE(max(version), 0) + 1 FROM plan_versions WHERE tenant_id = app_tenant_id() AND plan_id = @plan_id),
        @base_fee_minor)
RETURNING *;

-- name: InsertPlanPrice :exec
INSERT INTO plan_prices (tenant_id, plan_version_id, meter_code, model, config)
VALUES (app_tenant_id(), $1, $2, $3, $4);

-- name: InsertPlanEntitlement :exec
INSERT INTO plan_entitlements (tenant_id, plan_version_id, meter_code, limit_units, alert_thresholds_pct)
VALUES (app_tenant_id(), $1, $2, $3, $4);

-- name: GetPlanVersionByID :one
SELECT v.id, v.plan_id, v.version, v.base_fee_minor, v.published_at, p.code AS plan_code
FROM plan_versions v JOIN plans p ON p.tenant_id = v.tenant_id AND p.id = v.plan_id
WHERE v.tenant_id = app_tenant_id() AND v.id = $1;

-- name: GetPlanVersionByCode :one
-- version 0 = latest.
SELECT v.id, v.plan_id, v.version, v.base_fee_minor, v.published_at, p.code AS plan_code
FROM plan_versions v JOIN plans p ON p.tenant_id = v.tenant_id AND p.id = v.plan_id
WHERE v.tenant_id = app_tenant_id() AND p.code = @plan_code AND (@version::int = 0 OR v.version = @version::int)
ORDER BY v.version DESC
LIMIT 1;

-- name: ListPlanPrices :many
SELECT meter_code, model, config FROM plan_prices
WHERE tenant_id = app_tenant_id() AND plan_version_id = $1 ORDER BY meter_code;

-- name: ListPlanEntitlements :many
SELECT meter_code, limit_units, alert_thresholds_pct FROM plan_entitlements
WHERE tenant_id = app_tenant_id() AND plan_version_id = $1 ORDER BY meter_code;

-- name: InsertSubscription :one
INSERT INTO subscriptions (tenant_id, id, customer_id, plan_version_id, active, state, interval, currency, trial_ends_at, billing_anchor, changed_from_id, provider, provider_ref, owner, fee_timing, fee_due_at)
VALUES (app_tenant_id(), @id, @customer_id, @plan_version_id, tstzrange(@start_time::timestamptz, NULL, '[)'),
        @state, @interval, @currency, @trial_ends_at, @billing_anchor::timestamptz, @changed_from_id, @provider, @provider_ref, @owner, @fee_timing, sqlc.narg('fee_due_at')::timestamptz)
RETURNING id, customer_id, plan_version_id,
          lower(active)::timestamptz AS start_time,
          upper_inf(active)::bool AS open_ended,
          COALESCE(upper(active), lower(active))::timestamptz AS end_time,
          created_at, state, interval, currency, trial_ends_at, cancel_at_period_end, paused_at, billing_anchor, changed_from_id, provider, provider_ref, owner, fee_timing;

-- name: GetSubscription :one
SELECT s.id, s.customer_id, c.external_id AS customer_external_id, s.plan_version_id,
       lower(s.active)::timestamptz AS start_time,
       upper_inf(s.active)::bool AS open_ended,
       COALESCE(upper(s.active), lower(s.active))::timestamptz AS end_time,
       s.created_at, s.state, s.interval, s.currency, s.trial_ends_at, s.cancel_at_period_end, s.paused_at, s.billing_anchor, s.changed_from_id, s.provider, s.provider_ref, s.owner, s.fee_timing, pl.code AS plan_code, pv.version AS plan_version
FROM subscriptions s JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id
     JOIN plan_versions pv ON pv.tenant_id = s.tenant_id AND pv.id = s.plan_version_id
     JOIN plans pl ON pl.tenant_id = pv.tenant_id AND pl.id = pv.plan_id
WHERE s.tenant_id = app_tenant_id() AND s.id = $1;

-- name: GetSubscriptionForUpdate :one
SELECT s.id, s.customer_id, c.external_id AS customer_external_id, s.plan_version_id,
       lower(s.active)::timestamptz AS start_time,
       upper_inf(s.active)::bool AS open_ended,
       COALESCE(upper(s.active), lower(s.active))::timestamptz AS end_time,
       s.created_at, s.state, s.interval, s.currency, s.trial_ends_at, s.cancel_at_period_end, s.paused_at, s.billing_anchor, s.changed_from_id, s.provider, s.provider_ref, s.owner, s.fee_timing, pl.code AS plan_code, pv.version AS plan_version
FROM subscriptions s JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id
     JOIN plan_versions pv ON pv.tenant_id = s.tenant_id AND pv.id = s.plan_version_id
     JOIN plans pl ON pl.tenant_id = pv.tenant_id AND pl.id = pv.plan_id
WHERE s.tenant_id = app_tenant_id() AND s.id = $1
FOR UPDATE OF s;

-- name: LinkProviderSubscription :execrows
-- Records the provider's id of a subscription and who owns it. An event older than the last one applied changes nothing.
UPDATE subscriptions SET provider = @provider, provider_ref = @provider_ref, owner = @owner, provider_synced_at = @occurred_at::timestamptz
WHERE tenant_id = app_tenant_id() AND id = @id AND (provider_synced_at IS NULL OR provider_synced_at <= @occurred_at::timestamptz);

-- name: SetSubscriptionState :execrows
UPDATE subscriptions SET state = @state, paused_at = @paused_at, cancel_at_period_end = @cancel_at_period_end
WHERE tenant_id = app_tenant_id() AND id = @id;

-- name: EndSubscription :execrows
UPDATE subscriptions SET active = tstzrange(lower(active), @end_time::timestamptz, '[)')
WHERE tenant_id = app_tenant_id() AND id = @id AND upper_inf(active) AND @end_time::timestamptz > lower(active);

-- name: ListSubscriptionsOverlapping :many
SELECT s.id, s.customer_id, c.external_id AS customer_external_id, s.plan_version_id,
       lower(s.active)::timestamptz AS start_time,
       upper_inf(s.active)::bool AS open_ended,
       COALESCE(upper(s.active), lower(s.active))::timestamptz AS end_time,
       s.created_at, s.state, s.interval, s.currency, s.trial_ends_at, s.cancel_at_period_end, s.paused_at, s.billing_anchor, s.changed_from_id, s.provider, s.provider_ref, s.owner, s.fee_timing, pl.code AS plan_code, pv.version AS plan_version
FROM subscriptions s JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id
     JOIN plan_versions pv ON pv.tenant_id = s.tenant_id AND pv.id = s.plan_version_id
     JOIN plans pl ON pl.tenant_id = pv.tenant_id AND pl.id = pv.plan_id
WHERE s.tenant_id = app_tenant_id()
  AND s.active && tstzrange(@period_start::timestamptz, @period_end::timestamptz, '[)')
  AND s.id > @after::uuid
ORDER BY s.id
LIMIT 500;

-- name: ReopenSubscription :execrows
-- Withdraws a scheduled end: the range becomes open-ended again.
UPDATE subscriptions SET active = tstzrange(lower(active), NULL, '[)')
WHERE tenant_id = app_tenant_id() AND id = @id AND NOT upper_inf(active) AND upper(active) > @now::timestamptz;

-- name: InsertPlanVersionFee :exec
INSERT INTO plan_version_fees (tenant_id, plan_version_id, currency, interval, base_fee_minor)
VALUES (app_tenant_id(), $1, $2, $3, $4);

-- name: ListPlanVersionFees :many
SELECT currency, interval, base_fee_minor FROM plan_version_fees
WHERE tenant_id = app_tenant_id() AND plan_version_id = $1 ORDER BY interval DESC, currency;

-- name: InsertPlanCurrencyPrice :exec
INSERT INTO plan_currency_prices (tenant_id, plan_version_id, currency, meter_code, model, config)
VALUES (app_tenant_id(), $1, $2, $3, $4, $5);

-- name: ListPlanCurrencyPrices :many
SELECT currency, meter_code, model, config FROM plan_currency_prices
WHERE tenant_id = app_tenant_id() AND plan_version_id = $1 ORDER BY currency, meter_code;

-- name: InsertOutbox :exec
-- A NULL payload is a Kafka tombstone.
INSERT INTO outbox (tenant_id, topic, partition_key, payload)
VALUES (app_tenant_id(), @topic, @partition_key, @payload);

-- name: NotifyOutbox :exec
SELECT pg_notify('outbox_new', '');

-- name: ListRoutingRules :many
SELECT country, currency, provider FROM routing_rules WHERE tenant_id = app_tenant_id() ORDER BY position;

-- name: DeleteRoutingRules :exec
DELETE FROM routing_rules WHERE tenant_id = app_tenant_id();

-- name: InsertRoutingRule :exec
INSERT INTO routing_rules (tenant_id, position, country, currency, provider) VALUES (app_tenant_id(), $1, $2, $3, $4);

-- name: InsertProviderMigration :one
INSERT INTO provider_migrations (tenant_id, id, subscription_id, from_provider, to_provider, state, cutover_at)
VALUES (app_tenant_id(), @id, @subscription_id, @from_provider, @to_provider, @state, @cutover_at)
RETURNING *;

-- name: GetProviderMigration :one
SELECT * FROM provider_migrations WHERE tenant_id = app_tenant_id() AND id = $1;

-- name: GetProviderMigrationForUpdate :one
SELECT * FROM provider_migrations WHERE tenant_id = app_tenant_id() AND id = $1 FOR UPDATE;

-- name: FinishProviderMigration :one
UPDATE provider_migrations SET state = @state, new_subscription_id = @new_subscription_id, cutover_at = @cutover_at, updated_at = now()
WHERE tenant_id = app_tenant_id() AND id = @id
RETURNING *;

-- name: ListSubscriptionsPage :many
-- Newest first (ids are time-ordered); after is the last id of the previous page, or the maximum uuid for the first.
SELECT s.id, s.customer_id, c.external_id AS customer_external_id, s.plan_version_id,
       lower(s.active)::timestamptz AS start_time,
       upper_inf(s.active)::bool AS open_ended,
       COALESCE(upper(s.active), lower(s.active))::timestamptz AS end_time,
       s.created_at, s.state, s.interval, s.currency, s.trial_ends_at, s.cancel_at_period_end, s.paused_at, s.billing_anchor, s.changed_from_id, s.provider, s.provider_ref, s.owner, s.fee_timing, pl.code AS plan_code, pv.version AS plan_version
FROM subscriptions s JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id
     JOIN plan_versions pv ON pv.tenant_id = s.tenant_id AND pv.id = s.plan_version_id
     JOIN plans pl ON pl.tenant_id = pv.tenant_id AND pl.id = pv.plan_id
WHERE s.tenant_id = app_tenant_id() AND s.id < @before::uuid AND (@customer_id::uuid = '00000000-0000-0000-0000-000000000000'::uuid OR s.customer_id = @customer_id::uuid)
ORDER BY s.id DESC
LIMIT @page_size;

-- name: ListSubscriptionFeesDue :many
-- Advance-billed subscriptions whose next period has started and still has time left (an open-ended one, or one ending after that period began).
SELECT id, fee_due_at::timestamptz AS fee_due_at FROM subscriptions
WHERE tenant_id = app_tenant_id() AND fee_timing = 'advance' AND owner = 'quotify' AND fee_due_at IS NOT NULL AND fee_due_at <= @now::timestamptz
  AND state <> 'cancelled' AND (upper_inf(active) OR upper(active) > fee_due_at) AND id > @after::uuid
ORDER BY id
LIMIT @page_size;

-- name: AdvanceSubscriptionFee :execrows
-- Moves the next period to invoice forward. Compare-and-set on the period that was invoiced, so a repeated call changes nothing.
UPDATE subscriptions SET fee_due_at = @next_due::timestamptz
WHERE tenant_id = app_tenant_id() AND id = @id AND fee_due_at = @period_start::timestamptz;

-- name: InsertSubscriptionChange :one
INSERT INTO subscription_changes (tenant_id, id, subscription_id, plan_version_id, interval, effective_at)
VALUES (app_tenant_id(), @id, @subscription_id, @plan_version_id, @interval, @effective_at)
RETURNING id, subscription_id, plan_version_id, interval, effective_at, state;

-- name: GetPendingChange :one
SELECT id, subscription_id, plan_version_id, interval, effective_at, state FROM subscription_changes
WHERE tenant_id = app_tenant_id() AND subscription_id = $1 AND state = 'pending';

-- name: ListPendingChangesFor :many
SELECT c.id, c.subscription_id, c.plan_version_id, c.interval, c.effective_at, pl.code AS plan_code, pv.version AS plan_version
FROM subscription_changes c
JOIN plan_versions pv ON pv.tenant_id = c.tenant_id AND pv.id = c.plan_version_id
JOIN plans pl ON pl.tenant_id = pv.tenant_id AND pl.id = pv.plan_id
WHERE c.tenant_id = app_tenant_id() AND c.state = 'pending' AND c.subscription_id = ANY(@ids::uuid[]);

-- name: ListDueChanges :many
SELECT id, subscription_id, plan_version_id, interval, effective_at, state FROM subscription_changes
WHERE tenant_id = app_tenant_id() AND state = 'pending' AND effective_at <= @now::timestamptz
ORDER BY effective_at, id
LIMIT 100
FOR UPDATE;

-- name: SettleChange :execrows
UPDATE subscription_changes SET state = @state, applied_at = CASE WHEN @state = 'applied' THEN now() END, new_subscription_id = @new_subscription_id
WHERE tenant_id = app_tenant_id() AND id = @id AND state = 'pending';

-- name: InsertSubscriptionAdjustment :exec
INSERT INTO subscription_adjustments (tenant_id, id, subscription_id, old_plan_code, from_at, to_at, credit_minor, charge_minor)
VALUES (app_tenant_id(), @id, @subscription_id, @old_plan_code, @from_at, @to_at, @credit_minor, @charge_minor);

-- name: ListUnbilledAdjustments :many
SELECT id, subscription_id, old_plan_code, from_at, to_at, credit_minor, charge_minor FROM subscription_adjustments
WHERE tenant_id = app_tenant_id() AND billed_at IS NULL
ORDER BY created_at, id
LIMIT @page_size;

-- name: MarkAdjustmentBilled :execrows
UPDATE subscription_adjustments SET billed_at = now() WHERE tenant_id = app_tenant_id() AND id = @id AND billed_at IS NULL;

-- name: GetSubscriptionFeeDue :one
SELECT fee_due_at FROM subscriptions WHERE tenant_id = app_tenant_id() AND id = $1;

-- name: ReleaseProviderLink :execrows
-- An ended subscription gives its provider id to its successor (the id is unique); it keeps the provider's name for history.
UPDATE subscriptions SET owner = 'quotify', provider_ref = NULL WHERE tenant_id = app_tenant_id() AND id = $1;

-- name: GetSubscriptionIDByProviderRef :one
-- The live subscription that holds a provider's id: after plan changes it is the last of the chain, not the one that was first linked.
SELECT id FROM subscriptions WHERE tenant_id = app_tenant_id() AND provider = $1 AND provider_ref = $2;

-- name: GetChangedFromID :one
SELECT changed_from_id FROM subscriptions WHERE tenant_id = app_tenant_id() AND id = $1;
