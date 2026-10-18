-- name: LoadOffsets :many
SELECT partition, next_offset
FROM consumer_offsets
WHERE consumer_group = @consumer_group AND topic = @topic AND partition = ANY(@partitions::int[]);

-- name: ListOffsets :many
SELECT partition, next_offset
FROM consumer_offsets
WHERE consumer_group = @consumer_group AND topic = @topic
ORDER BY partition;

-- name: EnsureProcessedPartition :exec
SELECT ensure_processed_partition(@period::text);

-- name: DropOldProcessedPartitions :one
SELECT drop_old_processed_partitions(@now::timestamptz, @retention::interval)::text[] AS dropped;

-- name: LockPeriodSettings :many
-- The batch transaction holds these rows FOR SHARE so that ClosePeriodEarly and FreezePeriod wait for it.
SELECT tenant_id, period_id, timezone, cutoff_at, frozen_at
FROM period_settings
WHERE tenant_id = ANY(@tenants::uuid[]) AND period_id = ANY(@periods::text[])
FOR SHARE;

-- name: PeriodSettingsExist :many
SELECT tenant_id, period_id
FROM period_settings
WHERE tenant_id = ANY(@tenants::uuid[]) AND period_id = ANY(@periods::text[]);

-- name: InsertPeriodSettings :exec
INSERT INTO period_settings (tenant_id, period_id, timezone, period_start, period_end, grace_period, cutoff_at)
VALUES (@tenant_id, @period_id, @timezone, @period_start, @period_end, make_interval(secs => @grace_seconds::float8), @cutoff_at)
ON CONFLICT DO NOTHING;

-- name: LockGenerations :many
SELECT tenant_id, generation, status
FROM aggregate_generations
WHERE tenant_id = ANY(@tenants::uuid[]) AND status IN ('active', 'catching_up')
FOR SHARE;

-- name: ActiveGeneration :one
SELECT COALESCE((SELECT generation FROM aggregate_generations WHERE tenant_id = app_tenant_id() AND status = 'active'), 1)::int AS generation;

-- name: CustomerUsage :many
SELECT customer_id, meter_code, period_id, quantity::text AS quantity, quantity_scale::int AS quantity_scale, event_count
FROM usage_aggregates
WHERE customer_id = @customer_id AND period_id = @period_id AND generation = @generation
ORDER BY meter_code;

-- name: PeriodUsage :many
SELECT customer_id, meter_code, period_id, quantity::text AS quantity, quantity_scale::int AS quantity_scale, event_count
FROM usage_aggregates
WHERE period_id = @period_id AND generation = @generation
ORDER BY customer_id, meter_code;

-- name: LateUsageBilledIn :many
SELECT customer_id, meter_code, origin_period_id, billed_period_id, quantity::text AS quantity, quantity_scale::int AS quantity_scale, event_count
FROM late_usage
WHERE billed_period_id = @billed_period_id AND generation = @generation
ORDER BY customer_id, meter_code, origin_period_id;

-- name: EnsurePeriodSettingsApp :exec
INSERT INTO period_settings (tenant_id, period_id, timezone, period_start, period_end, grace_period, cutoff_at)
VALUES (app_tenant_id(), @period_id, @timezone, @period_start, @period_end, make_interval(secs => @grace_seconds::float8), @cutoff_at)
ON CONFLICT DO NOTHING;

-- name: FreezePeriod :one
UPDATE period_settings
SET frozen_at = COALESCE(frozen_at, clock_timestamp())
WHERE tenant_id = app_tenant_id() AND period_id = @period_id
RETURNING frozen_at;

-- name: ClosePeriodEarly :one
UPDATE period_settings
SET cutoff_at = clock_timestamp() + interval '5 seconds', early_closed_at = clock_timestamp()
WHERE tenant_id = app_tenant_id() AND period_id = @period_id
  AND frozen_at IS NULL AND cutoff_at > clock_timestamp() + interval '5 seconds'
RETURNING cutoff_at;
