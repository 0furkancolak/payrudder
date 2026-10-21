-- Invoices and their lines. The tenant-scoped reads run as billing_app inside pgxx.InTenantTx (RLS filters by tenant); the
-- writes run as billing_system with an explicit tenant_id.

-- name: GetInvoice :one
SELECT i.id, i.customer_id, COALESCE(i.snapshot->>'customer_external_id', '')::text AS customer_external_id, i.period_id, i.kind,
       COALESCE(i.number, '')::text AS number, i.plan_version_id, i.currency, i.subtotal_minor, i.total_minor, i.status,
       COALESCE(s.state, '')::text AS saga_state, COALESCE(i.pdf_object_key, '')::text AS pdf_object_key, i.created_at, i.finalized_at
FROM invoices i LEFT JOIN invoice_sagas s ON s.tenant_id = i.tenant_id AND s.invoice_id = i.id
WHERE i.id = @id;

-- name: ListInvoices :many
SELECT i.id, i.customer_id, COALESCE(i.snapshot->>'customer_external_id', '')::text AS customer_external_id, i.period_id, i.kind,
       COALESCE(i.number, '')::text AS number, i.plan_version_id, i.currency, i.subtotal_minor, i.total_minor, i.status,
       COALESCE(s.state, '')::text AS saga_state, COALESCE(i.pdf_object_key, '')::text AS pdf_object_key, i.created_at, i.finalized_at
FROM invoices i LEFT JOIN invoice_sagas s ON s.tenant_id = i.tenant_id AND s.invoice_id = i.id
WHERE (sqlc.narg('customer_id')::uuid IS NULL OR i.customer_id = sqlc.narg('customer_id'))
  AND (sqlc.narg('period_id')::text IS NULL OR i.period_id = sqlc.narg('period_id'))
  AND (sqlc.narg('status')::text IS NULL OR i.status = sqlc.narg('status'))
  AND i.id > @after::uuid
ORDER BY i.id
LIMIT @page_size;

-- name: ListInvoiceLines :many
SELECT line_no, kind, COALESCE(meter_code, '')::text AS meter_code, COALESCE(origin_period_id, '')::text AS origin_period_id,
       COALESCE(quantity::text, '')::text AS quantity, quantity_scale, amount_minor, description, service_from, service_to
FROM invoice_lines WHERE invoice_id = @invoice_id ORDER BY line_no;

-- name: ListInvoiceLinesForSnapshot :many
SELECT line_no, kind, description, meter_code, COALESCE(quantity::text, '')::text AS quantity, quantity_scale, unit_label, amount_minor
FROM invoice_lines WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id ORDER BY line_no;

-- name: GetInvoicePDFKey :one
SELECT pdf_object_key FROM invoices WHERE id = @id;

-- name: GetInvoiceStatus :one
SELECT status FROM invoices WHERE id = @id;

-- name: InvoiceExists :one
SELECT EXISTS (SELECT 1 FROM invoices WHERE id = @id);

-- name: GetInvoiceHostedFields :one
SELECT COALESCE(snapshot->>'tenant_name', '')::text AS seller_name, COALESCE(snapshot->>'customer_name', '')::text AS customer_name,
       COALESCE((snapshot->>'currency_exponent')::int, 2)::int AS currency_exponent
FROM invoices WHERE id = @id;

-- name: GetInvoiceAddresses :one
SELECT COALESCE(snapshot->'seller_address', '[]'::jsonb)::jsonb AS seller_address, COALESCE(snapshot->'customer_address', '[]'::jsonb)::jsonb AS customer_address
FROM invoices WHERE id = @id;

-- name: GetInvoiceBankTransfer :one
SELECT COALESCE(number, '')::text AS number, COALESCE(snapshot->'bank_transfer', 'null'::jsonb)::jsonb AS bank_transfer FROM invoices WHERE id = @id;

-- name: GetInvoiceCustomerExternalID :one
SELECT COALESCE(snapshot->>'customer_external_id', '')::text AS customer_external_id FROM invoices WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetInvoiceIDByFeeKey :one
SELECT id FROM invoices WHERE tenant_id = @tenant_id AND fee_key = @fee_key::text;

-- name: InsertDraftInvoice :one
INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot,
                      provider, country, fee_key, rerating_job_id)
VALUES (@tenant_id, @id, @customer_id, @period_id, @kind, @plan_version_id, @currency, @subtotal_minor, @total_minor, 'draft', @snapshot,
        @provider, sqlc.narg('country'), sqlc.narg('fee_key'), sqlc.narg('rerating_job_id'))
ON CONFLICT DO NOTHING
RETURNING id;

-- name: InsertInvoiceLine :exec
INSERT INTO invoice_lines (tenant_id, invoice_id, line_no, kind, meter_code, origin_period_id, quantity, quantity_scale, unit_label, unit_price,
                           amount_minor, description, service_from, service_to)
VALUES (@tenant_id, @invoice_id, @line_no, @kind, sqlc.narg('meter_code'), sqlc.narg('origin_period_id'), sqlc.narg('quantity')::text::numeric,
        sqlc.narg('quantity_scale'), sqlc.narg('unit_label'), sqlc.narg('unit_price')::text::numeric, @amount_minor, @description,
        sqlc.narg('service_from'), sqlc.narg('service_to'));

-- name: SetInvoiceTotals :exec
UPDATE invoices SET subtotal_minor = @subtotal_minor, total_minor = @total_minor WHERE tenant_id = @tenant_id AND id = @id;

-- name: SetInvoiceStatus :exec
UPDATE invoices SET status = @status WHERE tenant_id = @tenant_id AND id = @id;

-- name: SetInvoicePDFKeyBySaga :exec
UPDATE invoices AS inv SET pdf_object_key = @pdf_object_key::text
WHERE inv.tenant_id = @tenant_id AND inv.id = (SELECT s.invoice_id FROM invoice_sagas s WHERE s.tenant_id = @tenant_id AND s.id = @saga_id);

-- name: LockInvoiceForFinalize :one
SELECT status, (snapshot->>'environment')::text AS environment, currency, period_id, total_minor, customer_id,
       COALESCE(snapshot->>'customer_external_id', '')::text AS customer_external_id
FROM invoices WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- name: EnsureInvoiceNumberSeq :exec
INSERT INTO invoice_number_seq (tenant_id, year, next_value) VALUES (@tenant_id, @year, 1) ON CONFLICT DO NOTHING;

-- name: LockInvoiceNumberSeq :one
SELECT next_value FROM invoice_number_seq WHERE tenant_id = @tenant_id AND year = @year FOR UPDATE;

-- name: BumpInvoiceNumberSeq :exec
UPDATE invoice_number_seq SET next_value = next_value + 1 WHERE tenant_id = @tenant_id AND year = @year;

-- name: OpenInvoice :exec
UPDATE invoices SET number = @number::text, status = 'open', finalized_at = @finalized_at::timestamptz WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListDraftInvoiceIDsOfRun :many
SELECT id FROM invoices
WHERE tenant_id = @tenant_id AND period_id = @period_id AND kind = 'REGULAR' AND status = 'draft'
ORDER BY id LIMIT @page_size;

-- name: ListDraftInvoiceIDsOfRerating :many
SELECT id FROM invoices WHERE tenant_id = @tenant_id AND rerating_job_id = @rerating_job_id::uuid AND status = 'draft' ORDER BY id;

-- name: CountUnsettledInvoicesOfRun :one
SELECT count(*) FROM invoices i LEFT JOIN invoice_sagas s ON s.tenant_id = i.tenant_id AND s.invoice_id = i.id
WHERE i.tenant_id = @tenant_id AND i.period_id = @period_id AND i.kind = 'REGULAR' AND (s.state IS NULL OR s.state NOT IN ('COMPLETED','VOIDED'));

-- name: PriorLateUsage :many
SELECT i.customer_id::text AS customer_id, l.meter_code, l.origin_period_id, sum(l.quantity)::text AS quantity, max(l.quantity_scale)::smallint AS quantity_scale
FROM invoice_lines l JOIN invoices i ON i.tenant_id = l.tenant_id AND i.id = l.invoice_id
WHERE l.tenant_id = @tenant_id AND l.kind = 'LATE_USAGE' AND l.origin_period_id = ANY(@origins::text[]) AND i.period_id <> @period_id AND i.status <> 'void'
GROUP BY 1, 2, 3;

-- name: BilledComponents :many
SELECT i.customer_id::text AS customer_id, COALESCE(l.meter_code, '')::text AS meter_code, sum(l.amount_minor)::bigint AS amount_minor
FROM invoices i JOIN invoice_lines l ON l.tenant_id = i.tenant_id AND l.invoice_id = i.id
WHERE i.tenant_id = @tenant_id AND i.period_id = @period_id AND i.kind IN ('REGULAR','CORRECTION') AND i.status <> 'void'
  AND l.kind IN ('BASE_FEE','USAGE','CORRECTION')
GROUP BY 1, 2;

-- name: ListRegularInvoiceSnapshots :many
SELECT customer_id::text AS customer_id, snapshot FROM invoices
WHERE tenant_id = @tenant_id AND period_id = @period_id AND kind = 'REGULAR' AND status <> 'void';
