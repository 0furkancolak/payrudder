-- An app's binding to an organisation's e-mail account (docs/plans/10, ADR 0026): a row of tenant_mail_integrations that names the shared account and holds no credential.
-- The twin of ../00039_organisation_mail_accounts.sql for a tenant placed in a cell; the shared account itself stays in the pooled database (a global table).

-- +goose Up
ALTER TABLE tenant_mail_integrations ADD COLUMN shared_account_id uuid;
ALTER TABLE tenant_mail_integrations ALTER COLUMN api_key_enc DROP NOT NULL;
ALTER TABLE tenant_mail_integrations DROP CONSTRAINT tenant_mail_integrations_base_url_check;
ALTER TABLE tenant_mail_integrations DROP CONSTRAINT tenant_mail_integrations_from_address_check;
ALTER TABLE tenant_mail_integrations ADD CONSTRAINT tenant_mail_integrations_own_or_shared CHECK (
  (shared_account_id IS NULL AND api_key_enc IS NOT NULL AND base_url <> '' AND from_address <> '')
  OR (shared_account_id IS NOT NULL AND api_key_enc IS NULL AND base_url = '' AND from_address = ''));

-- +goose Down
ALTER TABLE tenant_mail_integrations NO FORCE ROW LEVEL SECURITY; -- the owner sees every tenant's rows while it is off
DELETE FROM tenant_mail_integrations WHERE shared_account_id IS NOT NULL;
ALTER TABLE tenant_mail_integrations FORCE ROW LEVEL SECURITY;
ALTER TABLE tenant_mail_integrations DROP CONSTRAINT tenant_mail_integrations_own_or_shared;
ALTER TABLE tenant_mail_integrations ADD CONSTRAINT tenant_mail_integrations_base_url_check CHECK (base_url <> '');
ALTER TABLE tenant_mail_integrations ADD CONSTRAINT tenant_mail_integrations_from_address_check CHECK (from_address <> '');
ALTER TABLE tenant_mail_integrations ALTER COLUMN api_key_enc SET NOT NULL;
ALTER TABLE tenant_mail_integrations DROP COLUMN shared_account_id;
