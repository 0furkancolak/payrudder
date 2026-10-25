-- name: GetReplayJob :one
SELECT * FROM replay_jobs WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetReplayJobForUpdate :one
SELECT * FROM replay_jobs WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- name: ListOpenReplayJobs :many
SELECT * FROM replay_jobs WHERE status IN ('running', 'catching_up') ORDER BY created_at;

-- name: ReplayJobStatus :one
SELECT status FROM replay_jobs WHERE tenant_id = @tenant_id AND id = @id;

-- name: EnsureActiveGenerationOne :exec
-- A tenant without rows is on generation 1: make that explicit, so there is an active row to lock.
INSERT INTO aggregate_generations (tenant_id, generation, status) VALUES (@tenant_id, 1, 'active')
ON CONFLICT DO NOTHING;

-- name: TenantActiveGeneration :one
SELECT generation FROM aggregate_generations WHERE tenant_id = @tenant_id AND status = 'active';

-- name: NextGeneration :one
SELECT (COALESCE(max(generation), 0) + 1)::int AS generation FROM aggregate_generations WHERE tenant_id = @tenant_id;

-- name: InsertBuildingGeneration :exec
INSERT INTO aggregate_generations (tenant_id, generation, status) VALUES (@tenant_id, @generation, 'building');

-- name: InsertReplayJob :exec
INSERT INTO replay_jobs (id, tenant_id, from_period_id, to_period_id, target_generation, source_generation, status, start_offsets, end_offsets, reason)
VALUES (@id, @tenant_id, @from_period_id, @to_period_id, @target_generation, @source_generation, 'running', @start_offsets, @end_offsets, @reason);

-- name: FailReplayJob :exec
UPDATE replay_jobs SET status = 'failed', last_error = @last_error::text, updated_at = now(), completed_at = now()
WHERE tenant_id = @tenant_id AND id = @id AND status IN ('running', 'catching_up');

-- name: DiscardOpenGeneration :exec
UPDATE aggregate_generations SET status = 'discarded'
WHERE tenant_id = @tenant_id AND generation = @generation AND status IN ('building', 'catching_up');

-- name: TryReplayLock :one
SELECT pg_try_advisory_lock(@key::bigint) AS locked;

-- name: ReplayUnlock :exec
SELECT pg_advisory_unlock(@key::bigint);

-- name: MarkReplayReady :exec
UPDATE replay_jobs SET status = 'ready', verification = @verification, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id AND status = 'catching_up';

-- name: LockActiveGeneration :one
SELECT generation FROM aggregate_generations WHERE tenant_id = @tenant_id AND status = 'active' FOR UPDATE;

-- name: MarkGenerationCatchingUp :execrows
UPDATE aggregate_generations SET status = 'catching_up'
WHERE tenant_id = @tenant_id AND generation = @generation AND status = 'building';

-- name: MarkReplayCatchingUp :exec
UPDATE replay_jobs SET status = 'catching_up', dual_write_from = @dual_write_from, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id AND status = 'running';

-- name: LockLiveGenerations :exec
-- Waits for the live batches that hold the tenant's generation rows FOR SHARE; new ones wait for this transaction.
SELECT 1 FROM aggregate_generations WHERE tenant_id = @tenant_id AND status IN ('active', 'catching_up') FOR UPDATE;

-- name: RetireActiveGeneration :exec
UPDATE aggregate_generations SET status = 'retired' WHERE tenant_id = @tenant_id AND generation = @generation AND status = 'active';

-- name: ActivateGeneration :execrows
UPDATE aggregate_generations SET status = 'active', promoted_at = now()
WHERE tenant_id = @tenant_id AND generation = @generation AND status = 'catching_up';

-- name: MarkReplayPromoted :exec
UPDATE replay_jobs SET status = 'promoted', updated_at = now(), completed_at = now() WHERE tenant_id = @tenant_id AND id = @id;

-- name: DiscardGeneration :exec
UPDATE aggregate_generations SET status = 'discarded' WHERE tenant_id = @tenant_id AND generation = @generation;

-- name: MarkReplayDiscarded :exec
UPDATE replay_jobs SET status = 'discarded', updated_at = now(), completed_at = now() WHERE tenant_id = @tenant_id AND id = @id;

-- name: DeleteGenerationUsage :exec
DELETE FROM usage_aggregates WHERE tenant_id = @tenant_id AND generation = @generation;

-- name: DeleteGenerationLateUsage :exec
DELETE FROM late_usage WHERE tenant_id = @tenant_id AND generation = @generation;

-- name: DeleteGenerationProcessedEvents :exec
DELETE FROM processed_events WHERE tenant_id = @tenant_id AND generation = @generation;

-- name: CompareUsage :many
-- The live generation (a) against the rebuilt one (b) over the periods [from, to]; in_old and in_new say in which generation the row exists.
SELECT COALESCE(a.period_id, b.period_id)::text AS period_id, COALESCE(a.customer_id, b.customer_id)::text AS customer_id,
       COALESCE(a.meter_code, b.meter_code)::text AS meter_code, ''::text AS origin_period_id,
       COALESCE(a.quantity::text, '')::text AS old_quantity, COALESCE(b.quantity::text, '')::text AS new_quantity,
       (a.event_count IS NOT NULL)::boolean AS in_old, (b.event_count IS NOT NULL)::boolean AS in_new,
       COALESCE(a.event_count, 0)::bigint AS old_event_count, COALESCE(b.event_count, 0)::bigint AS new_event_count
FROM (SELECT x.period_id, x.customer_id, x.meter_code, x.quantity, x.event_count FROM usage_aggregates x
      WHERE x.tenant_id = @tenant_id AND x.generation = @source AND x.period_id BETWEEN @from_period::text AND @to_period::text) a
FULL OUTER JOIN (SELECT y.period_id, y.customer_id, y.meter_code, y.quantity, y.event_count FROM usage_aggregates y
      WHERE y.tenant_id = @tenant_id AND y.generation = @target AND y.period_id BETWEEN @from_period::text AND @to_period::text) b
  USING (period_id, customer_id, meter_code)
ORDER BY 1, 2, 3;

-- name: CompareLateUsage :many
-- As CompareUsage, over the late usage whose origin period is in [from, to]; period_id is the billed period.
SELECT COALESCE(a.billed_period_id, b.billed_period_id)::text AS period_id, COALESCE(a.customer_id, b.customer_id)::text AS customer_id,
       COALESCE(a.meter_code, b.meter_code)::text AS meter_code, COALESCE(a.origin_period_id, b.origin_period_id)::text AS origin_period_id,
       COALESCE(a.quantity::text, '')::text AS old_quantity, COALESCE(b.quantity::text, '')::text AS new_quantity,
       (a.event_count IS NOT NULL)::boolean AS in_old, (b.event_count IS NOT NULL)::boolean AS in_new,
       COALESCE(a.event_count, 0)::bigint AS old_event_count, COALESCE(b.event_count, 0)::bigint AS new_event_count
FROM (SELECT x.billed_period_id, x.customer_id, x.meter_code, x.origin_period_id, x.quantity, x.event_count FROM late_usage x
      WHERE x.tenant_id = @tenant_id AND x.generation = @source AND x.origin_period_id BETWEEN @from_period::text AND @to_period::text) a
FULL OUTER JOIN (SELECT y.billed_period_id, y.customer_id, y.meter_code, y.origin_period_id, y.quantity, y.event_count FROM late_usage y
      WHERE y.tenant_id = @tenant_id AND y.generation = @target AND y.origin_period_id BETWEEN @from_period::text AND @to_period::text) b
  USING (billed_period_id, customer_id, meter_code, origin_period_id)
ORDER BY 1, 2, 3, 4;

-- name: ClearGenerationUsageOutside :exec
-- A replay rebuilds only [from, to]; the live consumer dual-writes every period, so the target holds a partial tail of the others.
DELETE FROM usage_aggregates WHERE tenant_id = @tenant_id AND generation = @generation AND (period_id < @from_period::text OR period_id > @to_period::text);

-- name: CopyGenerationUsageOutside :exec
INSERT INTO usage_aggregates (tenant_id, period_id, customer_id, meter_code, generation, quantity, quantity_scale, event_count, updated_at)
SELECT s.tenant_id, s.period_id, s.customer_id, s.meter_code, @target::int, s.quantity, s.quantity_scale, s.event_count, s.updated_at
FROM usage_aggregates s WHERE s.tenant_id = @tenant_id AND s.generation = @source AND (s.period_id < @from_period::text OR s.period_id > @to_period::text);

-- name: ClearGenerationLateUsageOutside :exec
DELETE FROM late_usage WHERE tenant_id = @tenant_id AND generation = @generation AND (origin_period_id < @from_period::text OR origin_period_id > @to_period::text);

-- name: CopyGenerationLateUsageOutside :exec
INSERT INTO late_usage (tenant_id, billed_period_id, customer_id, meter_code, origin_period_id, generation, quantity, quantity_scale, event_count, updated_at)
SELECT s.tenant_id, s.billed_period_id, s.customer_id, s.meter_code, s.origin_period_id, @target::int, s.quantity, s.quantity_scale, s.event_count, s.updated_at
FROM late_usage s WHERE s.tenant_id = @tenant_id AND s.generation = @source AND (s.origin_period_id < @from_period::text OR s.origin_period_id > @to_period::text);

-- name: ClearGenerationProcessedEventsOutside :exec
DELETE FROM processed_events WHERE tenant_id = @tenant_id AND generation = @generation AND (period_id < @from_period::text OR period_id > @to_period::text);

-- name: CopyGenerationProcessedEventsOutside :exec
-- The inbox of the periods outside the replay, so a redelivered old event is still recognised after the promotion.
INSERT INTO processed_events (tenant_id, event_id, generation, period_id)
SELECT s.tenant_id, s.event_id, @target::int, s.period_id
FROM processed_events s WHERE s.tenant_id = @tenant_id AND s.generation = @source AND (s.period_id < @from_period::text OR s.period_id > @to_period::text);
