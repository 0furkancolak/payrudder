-- Several e-mail provider accounts per app, a default among them, and the language of a mail naming the account that sends it. The twin of
-- ../00027_mail_accounts.sql for a tenant placed in a cell (docs/plans/10, ADR 0026).

-- +goose Up
-- Every row there is today is its tenant's only account, so it is the default: a column default fills them (an UPDATE would match nothing, the table forces row-level
-- security on its owner and the migration has no tenant), then the default for a new row is false.
ALTER TABLE tenant_mail_integrations ADD COLUMN is_default boolean NOT NULL DEFAULT true;
ALTER TABLE tenant_mail_integrations ALTER COLUMN is_default SET DEFAULT false;
ALTER TABLE tenant_mail_integrations DROP CONSTRAINT tenant_mail_integrations_provider_check;
ALTER TABLE tenant_mail_integrations ADD CONSTRAINT tenant_mail_integrations_provider_check CHECK (provider ~ '^nafru(~[a-z0-9][a-z0-9-]{0,30})?$');
ALTER TABLE tenant_mail_integrations DROP CONSTRAINT tenant_mail_integrations_pkey;
ALTER TABLE tenant_mail_integrations ADD PRIMARY KEY (tenant_id, provider);
CREATE UNIQUE INDEX one_default_mail_account ON tenant_mail_integrations (tenant_id) WHERE is_default;

CREATE TABLE tenant_mail_routing (
  tenant_id   uuid NOT NULL,
  language    text NOT NULL CHECK (language IN ('en', 'tr', 'de', 'fr', 'es', 'ar')),
  account     text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, language),
  FOREIGN KEY (tenant_id, account) REFERENCES tenant_mail_integrations (tenant_id, provider) ON DELETE CASCADE
);
ALTER TABLE tenant_mail_routing ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_mail_routing FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_mail_routing USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_mail_routing TO plans_app;

-- +goose Down
DROP TABLE tenant_mail_routing;
DROP INDEX one_default_mail_account;
ALTER TABLE tenant_mail_integrations NO FORCE ROW LEVEL SECURITY; -- the owner sees every tenant's rows while it is off
DELETE FROM tenant_mail_integrations WHERE NOT is_default;
ALTER TABLE tenant_mail_integrations DROP CONSTRAINT tenant_mail_integrations_pkey;
ALTER TABLE tenant_mail_integrations ADD PRIMARY KEY (tenant_id);
ALTER TABLE tenant_mail_integrations DROP CONSTRAINT tenant_mail_integrations_provider_check;
UPDATE tenant_mail_integrations SET provider = 'nafru';
ALTER TABLE tenant_mail_integrations FORCE ROW LEVEL SECURITY;
ALTER TABLE tenant_mail_integrations ADD CONSTRAINT tenant_mail_integrations_provider_check CHECK (provider = 'nafru');
ALTER TABLE tenant_mail_integrations DROP COLUMN is_default;
