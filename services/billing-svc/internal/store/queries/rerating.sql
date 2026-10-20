-- Re-rating jobs.

-- name: GetReratingJob :one
SELECT * FROM rerating_jobs WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetReratingJobByPlanVersion :one
SELECT * FROM rerating_jobs WHERE tenant_id = @tenant_id AND period_id = @period_id AND plan_version_id = @plan_version_id;

-- name: InsertReratingJob :execrows
INSERT INTO rerating_jobs (tenant_id, id, period_id, plan_version_id, generation, status) VALUES (@tenant_id, @id, @period_id, @plan_version_id, @generation, 'RUNNING')
ON CONFLICT (tenant_id, period_id, plan_version_id) DO NOTHING;

-- name: ListRunningReratingJobs :many
SELECT * FROM rerating_jobs WHERE status = 'RUNNING' AND created_at < @created_before::timestamptz ORDER BY created_at LIMIT 20;

-- name: FailReratingJob :exec
UPDATE rerating_jobs SET status = 'FAILED', last_error = @last_error::text WHERE tenant_id = @tenant_id AND id = @id;

-- name: SetReratingJobGeneration :exec
UPDATE rerating_jobs SET generation = @generation WHERE tenant_id = @tenant_id AND id = @id;

-- name: CompleteReratingJob :exec
UPDATE rerating_jobs SET status = 'COMPLETED', completed_at = @completed_at::timestamptz, last_error = NULL, customers_rated = @customers_rated,
  corrections = (SELECT count(*) FROM invoices i WHERE i.tenant_id = @tenant_id AND i.rerating_job_id = @id::uuid),
  correction_minor = COALESCE((SELECT sum(i.total_minor) FROM invoices i WHERE i.tenant_id = @tenant_id AND i.rerating_job_id = @id::uuid), 0),
  credits = (SELECT count(*) FROM credit_balances c WHERE c.tenant_id = @tenant_id AND c.source_rerating_job_id = @id::uuid),
  credit_minor = COALESCE((SELECT sum(c.amount_minor) FROM credit_balances c WHERE c.tenant_id = @tenant_id AND c.source_rerating_job_id = @id::uuid), 0)
WHERE tenant_id = @tenant_id AND id = @id;
