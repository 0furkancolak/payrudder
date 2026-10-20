-- name: UpsertEInvoiceAccount :exec
INSERT INTO einvoice_accounts (tenant_id, environment, provider, mode, settings_enc)
VALUES (@tenant_id, @environment, @provider, @mode, @settings_enc)
ON CONFLICT (tenant_id, environment) DO UPDATE
SET provider = EXCLUDED.provider, mode = EXCLUDED.mode, settings_enc = EXCLUDED.settings_enc, updated_at = now();

-- name: DeleteEInvoiceAccount :execrows
DELETE FROM einvoice_accounts WHERE tenant_id = @tenant_id AND environment = @environment;

-- name: ListEInvoiceAccounts :many
SELECT environment, provider, mode, updated_at FROM einvoice_accounts WHERE tenant_id = @tenant_id ORDER BY environment;

-- name: GetEInvoiceAccount :one
SELECT provider, mode, settings_enc, updated_at FROM einvoice_accounts WHERE tenant_id = @tenant_id AND environment = @environment;

-- name: BeginEInvoiceIssuance :execrows
INSERT INTO einvoice_issuances (document_id, tenant_id, environment, provider, kind, document_type, status)
VALUES (@document_id, @tenant_id, @environment, @provider, @kind, @document_type, 'started')
ON CONFLICT (document_id) DO NOTHING;

-- name: GetEInvoiceIssuance :one
SELECT document_id, tenant_id, environment, provider, kind, document_type, status, COALESCE(legal_number, '')::text AS legal_number, external_id,
       COALESCE(xml_key, '')::text AS xml_key, COALESCE(pdf_key, '')::text AS pdf_key
FROM einvoice_issuances WHERE document_id = @document_id AND tenant_id = @tenant_id;

-- name: CompleteEInvoiceIssuance :exec
UPDATE einvoice_issuances
SET status = 'issued', legal_number = @legal_number, external_id = @external_id, xml_key = @xml_key, pdf_key = @pdf_key, updated_at = now()
WHERE document_id = @document_id AND tenant_id = @tenant_id AND status = 'started';

-- name: CancelEInvoiceIssuance :exec
UPDATE einvoice_issuances SET status = 'cancelled', updated_at = now()
WHERE document_id = @document_id AND tenant_id = @tenant_id AND status = 'issued';
