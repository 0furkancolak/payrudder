-- name: UpsertEInvoiceAccount :exec
INSERT INTO einvoice_accounts (tenant_id, environment, provider, mode, settings_enc, is_default)
VALUES (@tenant_id, @environment, @provider, @mode, @settings_enc, @is_default)
ON CONFLICT (tenant_id, environment, provider) DO UPDATE
SET mode = EXCLUDED.mode, settings_enc = EXCLUDED.settings_enc, is_default = einvoice_accounts.is_default OR EXCLUDED.is_default, updated_at = now();

-- name: ClearDefaultEInvoiceAccounts :exec
UPDATE einvoice_accounts SET is_default = false
WHERE tenant_id = @tenant_id AND environment = @environment AND is_default AND provider <> @provider;

-- name: MarkEInvoiceAccountDefault :execrows
UPDATE einvoice_accounts SET is_default = true, updated_at = now()
WHERE tenant_id = @tenant_id AND environment = @environment AND provider = @provider;

-- name: CountEInvoiceAccounts :one
SELECT count(*) FROM einvoice_accounts WHERE tenant_id = @tenant_id AND environment = @environment;

-- name: DeleteEInvoiceAccount :execrows
DELETE FROM einvoice_accounts WHERE tenant_id = @tenant_id AND environment = @environment AND provider = @provider;

-- name: ListEInvoiceAccounts :many
SELECT environment, provider, mode, is_default, updated_at FROM einvoice_accounts WHERE tenant_id = @tenant_id ORDER BY environment, provider;

-- name: GetEInvoiceAccount :one
SELECT provider, mode, settings_enc, is_default, updated_at FROM einvoice_accounts WHERE tenant_id = @tenant_id AND environment = @environment AND provider = @provider;

-- name: GetDefaultEInvoiceAccount :one
SELECT provider, mode, settings_enc, is_default, updated_at FROM einvoice_accounts WHERE tenant_id = @tenant_id AND environment = @environment AND is_default;

-- name: ListEInvoiceRouting :many
SELECT country::text AS country, account FROM einvoice_routing WHERE tenant_id = @tenant_id AND environment = @environment ORDER BY country;

-- name: GetEInvoiceRoute :one
SELECT account FROM einvoice_routing WHERE tenant_id = @tenant_id AND environment = @environment AND country = @country;

-- name: DeleteEInvoiceRouting :exec
DELETE FROM einvoice_routing WHERE tenant_id = @tenant_id AND environment = @environment;

-- name: InsertEInvoiceRoute :exec
INSERT INTO einvoice_routing (tenant_id, environment, country, account) VALUES (@tenant_id, @environment, @country, @account);

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
