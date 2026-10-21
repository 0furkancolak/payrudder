-- E-documents of invoices and credit notes (phase 30). The request column is the frozen command and is read only where it is sent again.

-- name: InsertEInvoiceDocument :execrows
INSERT INTO einvoice_documents (tenant_id, id, kind, invoice_id, credit_note_id, number, environment, status, attempts, streak, last_dispatch_at, request, created_at, updated_at)
VALUES (@tenant_id, @id, @kind, @invoice_id, @credit_note_id, @number, @environment, 'pending', 1, 1, @now::timestamptz, @request, @now::timestamptz, @now::timestamptz)
ON CONFLICT DO NOTHING;

-- name: GetEInvoiceDocument :one
SELECT id, kind, invoice_id, credit_note_id, number, status, document_type, provider, environment, attempts, next_attempt_at, last_error_key, last_error,
       COALESCE(legal_number, '')::text AS legal_number, external_id, (xml_key IS NOT NULL)::bool AS has_xml, (pdf_key IS NOT NULL)::bool AS has_pdf,
       created_at, issued_at, updated_at
FROM einvoice_documents WHERE id = @id;

-- name: ListEInvoiceDocuments :many
SELECT id, kind, invoice_id, credit_note_id, number, status, document_type, provider, environment, attempts, next_attempt_at, last_error_key, last_error,
       COALESCE(legal_number, '')::text AS legal_number, external_id, (xml_key IS NOT NULL)::bool AS has_xml, (pdf_key IS NOT NULL)::bool AS has_pdf,
       created_at, issued_at, updated_at, count(*) OVER() AS _count
FROM einvoice_documents
WHERE (sqlc.narg('invoice_id')::uuid IS NULL OR invoice_id = sqlc.narg('invoice_id'))
  AND (sqlc.narg('credit_note_id')::uuid IS NULL OR credit_note_id = sqlc.narg('credit_note_id'))
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
  AND (created_at, id) < (@after_at::timestamptz, @after_id::uuid)
ORDER BY created_at DESC, id DESC
LIMIT @page_size;

-- name: GetEInvoiceFileKey :one
SELECT xml_key, pdf_key FROM einvoice_documents WHERE id = @id;

-- name: GetEInvoiceOfInvoice :one
SELECT id, status FROM einvoice_documents WHERE tenant_id = @tenant_id AND kind = 'invoice' AND invoice_id = @invoice_id;

-- name: LockEInvoiceDocument :one
SELECT kind, status, environment, attempts, streak, cancel_requested, provider, external_id, request
FROM einvoice_documents WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- name: SetEInvoiceIssued :exec
UPDATE einvoice_documents
SET status = 'issued', legal_number = @legal_number, external_id = @external_id, document_type = @document_type, provider = @provider, xml_key = @xml_key, pdf_key = @pdf_key,
    next_attempt_at = NULL, last_error_key = '', last_error = '', issued_at = COALESCE(issued_at, @now), updated_at = @now
WHERE tenant_id = @tenant_id AND id = @id AND status IN ('pending', 'failed');

-- name: SetEInvoiceRetry :exec
UPDATE einvoice_documents
SET next_attempt_at = @next_attempt_at, last_error_key = @error_key, last_error = @error, document_type = COALESCE(NULLIF(@document_type::text, ''), document_type),
    provider = COALESCE(NULLIF(@provider::text, ''), provider), updated_at = @now
WHERE tenant_id = @tenant_id AND id = @id AND status = 'pending';

-- name: SetEInvoiceFailed :exec
UPDATE einvoice_documents
SET status = 'failed', next_attempt_at = NULL, last_error_key = @error_key, last_error = @error, document_type = COALESCE(NULLIF(@document_type::text, ''), document_type),
    provider = COALESCE(NULLIF(@provider::text, ''), provider), updated_at = @now
WHERE tenant_id = @tenant_id AND id = @id AND status = 'pending';

-- name: SetEInvoiceCancelled :exec
UPDATE einvoice_documents SET status = 'cancelled', cancel_requested = false, last_error_key = '', last_error = '', updated_at = @now
WHERE tenant_id = @tenant_id AND id = @id AND status = 'issued';

-- name: SetEInvoiceCancelFailed :exec
UPDATE einvoice_documents SET cancel_requested = false, last_error_key = @error_key, last_error = @error, updated_at = @now
WHERE tenant_id = @tenant_id AND id = @id AND status = 'issued';

-- name: MarkEInvoiceDispatched :one
UPDATE einvoice_documents SET attempts = attempts + 1, streak = streak + 1, last_dispatch_at = @now::timestamptz, next_attempt_at = NULL, updated_at = @now
WHERE tenant_id = @tenant_id AND id = @id
RETURNING attempts;

-- name: ResetEInvoiceForRetry :exec
UPDATE einvoice_documents SET status = 'pending', streak = 0, request = @request, next_attempt_at = NULL, updated_at = @now
WHERE tenant_id = @tenant_id AND id = @id AND status = 'failed';

-- name: RequestEInvoiceCancel :exec
UPDATE einvoice_documents SET cancel_requested = true, last_dispatch_at = @now::timestamptz, last_error_key = '', last_error = '', updated_at = @now
WHERE tenant_id = @tenant_id AND id = @id AND status = 'issued';

-- name: ListDueEInvoices :many
SELECT tenant_id, id FROM einvoice_documents
WHERE (status = 'pending' AND ((next_attempt_at IS NOT NULL AND next_attempt_at <= @now::timestamptz)
                              OR (next_attempt_at IS NULL AND last_dispatch_at < @silent_before::timestamptz)))
   OR (status = 'issued' AND cancel_requested AND last_dispatch_at < @silent_before::timestamptz)
ORDER BY COALESCE(next_attempt_at, last_dispatch_at)
LIMIT @row_limit;

-- name: LockDueEInvoice :one
SELECT id FROM einvoice_documents
WHERE tenant_id = @tenant_id AND id = @id
  AND ((status = 'pending' AND ((next_attempt_at IS NOT NULL AND next_attempt_at <= @now::timestamptz)
                              OR (next_attempt_at IS NULL AND last_dispatch_at < @silent_before::timestamptz)))
    OR (status = 'issued' AND cancel_requested AND last_dispatch_at < @silent_before::timestamptz))
FOR UPDATE SKIP LOCKED;

-- name: GetEInvoiceExternalCustomer :one
SELECT COALESCE(i.snapshot->>'customer_external_id', '')::text AS customer_external_id
FROM einvoice_documents d JOIN invoices i ON i.tenant_id = d.tenant_id AND i.id = d.invoice_id
WHERE d.tenant_id = @tenant_id AND d.id = @id;
