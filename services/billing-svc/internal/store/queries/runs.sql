-- Billing runs, run-level locking and the gauge that counts runs.

-- name: GetBillingRun :one
SELECT * FROM billing_runs WHERE tenant_id = @tenant_id AND period_id = @period_id;

-- name: InsertBillingRun :exec
INSERT INTO billing_runs (tenant_id, period_id, status, cutoff_at) VALUES (@tenant_id, @period_id, 'SCHEDULED', @cutoff_at)
ON CONFLICT (tenant_id, period_id) DO NOTHING;

-- name: LowerRunCutoff :exec
UPDATE billing_runs SET cutoff_at = LEAST(cutoff_at, @cutoff_at)
WHERE tenant_id = @tenant_id AND period_id = @period_id AND status IN ('SCHEDULED','WAITING_WATERMARK');

-- name: SetRunStatus :exec
UPDATE billing_runs SET status = @status::text, last_error = NULLIF(@last_error::text, ''),
  started_at = COALESCE(started_at, CASE WHEN @status::text <> 'SCHEDULED' THEN now() END),
  completed_at = CASE WHEN @status::text = 'COMPLETED' THEN now() ELSE completed_at END
WHERE tenant_id = @tenant_id AND period_id = @period_id;

-- name: RetryFailedRun :execrows
UPDATE billing_runs SET status = 'RATING', last_error = NULL WHERE tenant_id = @tenant_id AND period_id = @period_id AND status = 'FAILED';

-- name: SetRunOffsets :exec
UPDATE billing_runs SET required_offsets = @required_offsets WHERE tenant_id = @tenant_id AND period_id = @period_id;

-- name: SetRunGeneration :exec
UPDATE billing_runs SET generation = @generation::int WHERE tenant_id = @tenant_id AND period_id = @period_id AND generation IS NULL;

-- name: SetRunInvoiceCount :exec
UPDATE billing_runs
SET invoice_count = (SELECT count(*) FROM invoices i WHERE i.tenant_id = @tenant_id AND i.period_id = @period_id AND i.kind = 'REGULAR')
WHERE tenant_id = @tenant_id AND period_id = @period_id;

-- name: ListDueRuns :many
SELECT tenant_id, period_id FROM billing_runs
WHERE status NOT IN ('COMPLETED','FAILED') AND cutoff_at + make_interval(secs => @delay_secs::float8) <= @now
ORDER BY cutoff_at;

-- name: CountRunsByStatus :many
SELECT status, count(*) AS runs FROM billing_runs GROUP BY status;

-- name: TryRunLock :one
SELECT pg_try_advisory_lock(hashtextextended(@key::text, 0))::bool AS locked;

-- name: ReleaseRunLock :exec
SELECT pg_advisory_unlock(hashtextextended(@key::text, 0));
