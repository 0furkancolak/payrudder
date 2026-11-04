-- Organisation-level e-mail accounts (ADR 0026): owned by an account (an organisation), read and written on the system pool, never by a tenant. The key is sealed with
-- an associated data that names the organisation and the account key.
-- name: ListOrganisationMailAccounts :many
SELECT * FROM organisation_mail_accounts WHERE account_id = @account_id ORDER BY provider;

-- name: GetOrganisationMailAccount :one
SELECT * FROM organisation_mail_accounts WHERE account_id = @account_id AND provider = @provider;

-- Binding and removing both take this lock, so an account is never removed in the instant an app binds to it.
-- name: LockOrganisationMailAccount :one
SELECT * FROM organisation_mail_accounts WHERE account_id = @account_id AND provider = @provider FOR UPDATE;

-- name: GetOrganisationMailAccountByID :one
SELECT * FROM organisation_mail_accounts WHERE id = @id;

-- name: UpsertOrganisationMailAccount :one
INSERT INTO organisation_mail_accounts (id, account_id, provider, base_url, api_key_enc, key_hint, from_address, checked_at)
VALUES (@id, @account_id, @provider, @base_url, @api_key_enc, @key_hint, @from_address, now())
ON CONFLICT (account_id, provider) DO UPDATE SET base_url = EXCLUDED.base_url, api_key_enc = EXCLUDED.api_key_enc, key_hint = EXCLUDED.key_hint,
  from_address = EXCLUDED.from_address, checked_at = EXCLUDED.checked_at, check_problem = '', updated_at = now()
RETURNING *;

-- name: DeleteOrganisationMailAccount :execrows
DELETE FROM organisation_mail_accounts WHERE id = @id;

-- name: MarkOrganisationMailAccountChecked :exec
UPDATE organisation_mail_accounts SET checked_at = CASE WHEN @problem::text = '' THEN now() ELSE checked_at END, check_problem = @problem::text WHERE id = @id;

-- An app's binding to an organisation's e-mail account (tenant scope): a row that names the shared account and holds no credential, so the default, the routing and the
-- removal rules of the app's own accounts apply to it unchanged.
-- name: InsertSharedMailBinding :one
INSERT INTO tenant_mail_integrations (tenant_id, provider, base_url, from_address, shared_account_id, is_default)
VALUES (app_tenant_id(), @provider, '', '', @shared_account_id, @is_default)
RETURNING *;

-- name: CountSharedMailBindings :one
SELECT count(*) FROM tenant_mail_integrations WHERE tenant_id = app_tenant_id() AND shared_account_id = @shared_account_id;
