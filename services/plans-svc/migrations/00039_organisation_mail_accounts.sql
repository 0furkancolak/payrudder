-- Organisation-level e-mail accounts (docs/plans/10, ADR 0026): one e-mail provider account that belongs to an organisation (an `accounts` row) and that several of its apps
-- use. Only e-mail: it is plans-svc's own, and no payment-path table is touched (payments and e-invoice accounts, the ledger, provider_objects, payment_attempts, subscriptions
-- and invoices stay as they are; that migration needs a rehearsal on real data and is not this one).
--
-- The account is a row in the pooled database that no tenant owns (a tenant placed in a cell cannot read it, and no `plans_app` grant exists: only the system role does, which
-- plans-svc's lookup uses after reading the tenant's binding). Its key is sealed with an associated data that names the organisation and the account key, never a tenant
-- (secretbox: "org-mail-account:<account id>:<key>"), so an app never holds the credential and a row copied to another organisation or key does not open.
--
-- An app binds to it through a row of its own tenant_mail_integrations (the twin of this migration): `shared_account_id` names the organisation's account and the row holds no
-- credential (api_key_enc is NULL, which the reseal job passes over), so the app's default, its routing by language and the rest of the existing logic work on it unchanged.

-- +goose Up
CREATE TABLE organisation_mail_accounts (
  id             uuid PRIMARY KEY,
  account_id     uuid NOT NULL REFERENCES accounts (id),
  provider       text NOT NULL CHECK (provider ~ '^nafru(~[a-z0-9][a-z0-9-]{0,30})?$'),
  base_url       text NOT NULL CHECK (base_url <> ''),
  api_key_enc    bytea NOT NULL,
  key_hint       text NOT NULL DEFAULT '',
  from_address   text NOT NULL CHECK (from_address <> ''),
  checked_at     timestamptz,
  check_problem  text NOT NULL DEFAULT '' CHECK (check_problem IN ('', 'unreachable', 'refused')),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (account_id, provider)
);
GRANT SELECT, INSERT, UPDATE, DELETE ON organisation_mail_accounts TO plans_system;

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
DROP TABLE organisation_mail_accounts;
