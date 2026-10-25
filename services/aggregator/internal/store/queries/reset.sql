-- Resetting a sandbox tenant (phase 45 E). Run as aggregator_system from internal/sandboxreset only; every statement names the tenant.

-- name: ResetUsageAggregates :execrows
DELETE FROM usage_aggregates WHERE tenant_id = $1;
-- name: ResetLateUsage :execrows
DELETE FROM late_usage WHERE tenant_id = $1;
-- What was processed: after a reset the same event is new again.
-- name: ResetProcessedEvents :execrows
DELETE FROM processed_events WHERE tenant_id = $1;
-- The cutoffs and early closes of the periods the tenant had.
-- name: ResetPeriodSettings :execrows
DELETE FROM period_settings WHERE tenant_id = $1;
