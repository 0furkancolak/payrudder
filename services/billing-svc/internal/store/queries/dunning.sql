-- Dunning settings (tenant-scoped, billing_app) and the dunning report.

-- name: GetDunningSettings :one
SELECT retry_hours, updated_at FROM dunning_settings WHERE tenant_id = @tenant_id;

-- name: UpsertDunningSettings :one
INSERT INTO dunning_settings (tenant_id, retry_hours) VALUES (@tenant_id, @retry_hours)
ON CONFLICT (tenant_id) DO UPDATE SET retry_hours = EXCLUDED.retry_hours, updated_at = now()
RETURNING updated_at;

-- name: DunningSummary :many
SELECT sg.state, i.provider, i.currency, count(*) AS invoices, COALESCE(sum(i.total_minor), 0)::bigint AS total_minor
FROM invoice_sagas sg JOIN invoices i ON i.tenant_id = sg.tenant_id AND i.id = sg.invoice_id
WHERE sg.state IN ('PAYMENT_DECLINED', 'PAYMENT_UNKNOWN', 'UNCOLLECTIBLE')
GROUP BY 1, 2, 3
ORDER BY 1, 2, 3;

-- name: DunningInvoices :many
SELECT i.id::text AS invoice_id, COALESCE(i.number, '')::text AS number, COALESCE(i.snapshot->>'customer_external_id', '')::text AS customer_external_id,
       i.period_id, i.provider, i.currency, i.total_minor, sg.state, sg.dunning_attempt, sg.next_attempt_at, COALESCE(sg.last_error, '')::text AS last_error
FROM invoice_sagas sg JOIN invoices i ON i.tenant_id = sg.tenant_id AND i.id = sg.invoice_id
WHERE sg.state IN ('PAYMENT_DECLINED', 'PAYMENT_UNKNOWN', 'UNCOLLECTIBLE')
ORDER BY i.created_at, i.id
LIMIT @row_limit;
