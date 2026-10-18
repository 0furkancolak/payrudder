-- Tenant-scoped queries, run as plans_app inside pgxx.InTenantTx. The explicit tenant_id = app_tenant_id() predicates
-- duplicate what RLS enforces anyway; they keep the (tenant_id, …) indexes obvious to the planner and to readers.

-- name: CurrentTenant :one
SELECT id, environment, tier, currency, timezone FROM current_tenant();

-- name: CreateCustomer :one
INSERT INTO customers (tenant_id, id, external_id, name, email, payment_method)
VALUES (app_tenant_id(), $1, $2, $3, $4, $5)
RETURNING *;

-- name: GetCustomerByExternalID :one
SELECT * FROM customers WHERE tenant_id = app_tenant_id() AND external_id = $1;

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
INSERT INTO subscriptions (tenant_id, id, customer_id, plan_version_id, active)
VALUES (app_tenant_id(), @id, @customer_id, @plan_version_id, tstzrange(@start_time::timestamptz, NULL, '[)'))
RETURNING id, customer_id, plan_version_id,
          lower(active)::timestamptz AS start_time,
          upper_inf(active)::bool AS open_ended,
          COALESCE(upper(active), lower(active))::timestamptz AS end_time,
          created_at;

-- name: GetSubscription :one
SELECT s.id, s.customer_id, c.external_id AS customer_external_id, s.plan_version_id,
       lower(s.active)::timestamptz AS start_time,
       upper_inf(s.active)::bool AS open_ended,
       COALESCE(upper(s.active), lower(s.active))::timestamptz AS end_time,
       s.created_at
FROM subscriptions s JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id
WHERE s.tenant_id = app_tenant_id() AND s.id = $1;

-- name: EndSubscription :execrows
UPDATE subscriptions SET active = tstzrange(lower(active), @end_time::timestamptz, '[)')
WHERE tenant_id = app_tenant_id() AND id = @id AND upper_inf(active) AND @end_time::timestamptz > lower(active);

-- name: ListSubscriptionsOverlapping :many
SELECT s.id, s.customer_id, c.external_id AS customer_external_id, s.plan_version_id,
       lower(s.active)::timestamptz AS start_time,
       upper_inf(s.active)::bool AS open_ended,
       COALESCE(upper(s.active), lower(s.active))::timestamptz AS end_time,
       s.created_at
FROM subscriptions s JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id
WHERE s.tenant_id = app_tenant_id()
  AND s.active && tstzrange(@period_start::timestamptz, @period_end::timestamptz, '[)')
  AND s.id > @after::uuid
ORDER BY s.id
LIMIT 500;

-- name: InsertOutbox :exec
-- A NULL payload is a Kafka tombstone.
INSERT INTO outbox (tenant_id, topic, partition_key, payload)
VALUES (app_tenant_id(), @topic, @partition_key, @payload);

-- name: NotifyOutbox :exec
SELECT pg_notify('outbox_new', '');
