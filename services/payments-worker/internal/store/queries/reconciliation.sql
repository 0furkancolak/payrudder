-- Reconciliation as a workflow (docs/plans/22 G7): the discrepancies a run finds, what became of them, and the runs. Every statement names the tenant.

-- name: LockReconciliation :exec
-- Two runs of one tenant and period (two replicas, an operator beside the schedule) take turns; the lock ends with the transaction.
SELECT pg_advisory_xact_lock(hashtextextended('payrudder reconciliation ' || @tenant_id::text || ' ' || @period::text, 0));

-- name: ListDiscrepanciesOfPeriod :many
SELECT * FROM reconciliation_discrepancies WHERE tenant_id = @tenant_id AND period = @period;

-- name: InsertDiscrepancy :one
INSERT INTO reconciliation_discrepancies (tenant_id, environment, period, fingerprint, kind, invoice_id, invoice_number, provider, provider_ref, currency, billed_minor, ledger_minor, settled_minor, detail, status)
VALUES (@tenant_id, @environment, @period, @fingerprint, @kind, @invoice_id, @invoice_number, @provider, @provider_ref, @currency, @billed_minor, @ledger_minor, @settled_minor, @detail, 'open')
ON CONFLICT (tenant_id, period, fingerprint) DO NOTHING
RETURNING id;

-- name: SeeDiscrepancy :exec
UPDATE reconciliation_discrepancies
SET invoice_number = @invoice_number, currency = @currency, billed_minor = @billed_minor, ledger_minor = @ledger_minor, settled_minor = @settled_minor, detail = @detail, last_seen_at = now()
WHERE tenant_id = @tenant_id AND id = @id;

-- name: ReopenDiscrepancy :exec
UPDATE reconciliation_discrepancies
SET status = 'open', resolution = '', resolved_at = NULL, invoice_number = @invoice_number, currency = @currency, billed_minor = @billed_minor, ledger_minor = @ledger_minor, settled_minor = @settled_minor,
    detail = @detail, last_seen_at = now(), updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id AND status = 'resolved';

-- name: ClearDiscrepancy :execrows
UPDATE reconciliation_discrepancies SET status = 'resolved', resolution = 'cleared', resolved_at = now(), updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id AND status <> 'resolved';

-- name: GetDiscrepancy :one
SELECT * FROM reconciliation_discrepancies WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetDiscrepancyForUpdate :one
SELECT * FROM reconciliation_discrepancies WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- name: AssignDiscrepancy :exec
UPDATE reconciliation_discrepancies SET owner = @owner, updated_at = now() WHERE tenant_id = @tenant_id AND id = @id;

-- name: ExplainDiscrepancy :exec
UPDATE reconciliation_discrepancies SET status = 'explained', note = @note, owner = @owner, resolution = '', resolved_at = NULL, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id;

-- name: ResolveDiscrepancy :exec
UPDATE reconciliation_discrepancies SET status = 'resolved', resolution = @resolution, note = @note, owner = @owner, resolved_at = now(), updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListDiscrepancies :many
SELECT * FROM reconciliation_discrepancies
WHERE tenant_id = @tenant_id
  AND (@period::text = '' OR period = @period)
  AND (cardinality(@statuses::text[]) = 0 OR status = ANY(@statuses::text[]))
  AND (cardinality(@kinds::text[]) = 0 OR kind = ANY(@kinds::text[]))
  AND (@owner::text = '' OR owner = @owner)
  AND (@invoice::text = '' OR invoice_id::text = @invoice)
  AND (@after_id::bigint = 0 OR id < @after_id::bigint)
ORDER BY id DESC LIMIT @row_limit::bigint;

-- name: CountDiscrepancies :many
SELECT status, kind, count(*)::bigint AS n FROM reconciliation_discrepancies
WHERE tenant_id = @tenant_id AND (@period::text = '' OR period = @period)
GROUP BY status, kind ORDER BY status, kind;

-- name: InsertReconciliationEvent :exec
INSERT INTO reconciliation_events (tenant_id, environment, discrepancy_id, action, actor, from_status, to_status, resolution, note)
VALUES (@tenant_id, @environment, @discrepancy_id, @action, @actor, @from_status, @to_status, @resolution, @note);

-- name: ListReconciliationEvents :many
SELECT id, action, actor, from_status, to_status, resolution, note, created_at FROM reconciliation_events
WHERE tenant_id = @tenant_id AND discrepancy_id = @discrepancy_id ORDER BY id;

-- name: InsertReconciliationRun :exec
INSERT INTO reconciliation_runs (tenant_id, environment, period, started_by, actor, result, paid_invoices, charges, found, opened, reopened, cleared, error)
VALUES (@tenant_id, @environment, @period, @started_by, @actor, @result, @paid_invoices, @charges, @found, @opened, @reopened, @cleared, @error);

-- name: ListReconciliationRuns :many
SELECT id, environment, period, started_by, actor, result, paid_invoices, charges, found, opened, reopened, cleared, error, finished_at FROM reconciliation_runs
WHERE tenant_id = @tenant_id ORDER BY id DESC LIMIT @row_limit::bigint;

-- name: LiveChargeTenants :many
-- The tenants the schedule visits: those that have taken a live payment through a provider. In a tenant's schema in a cell this is that tenant alone.
SELECT DISTINCT tenant_id FROM payment_ledger WHERE environment = 'live' AND kind = 'charge' AND status = 'succeeded';
