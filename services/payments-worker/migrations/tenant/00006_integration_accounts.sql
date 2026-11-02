-- payments_db, tenant schema: several e-invoice integrator accounts, routing by the buyer's country and the audit trail's kind (docs/plans/10, ADR 0026). The same
-- change as public 00005_integration_accounts.sql, written for a schema.

-- +goose Up
ALTER TABLE einvoice_accounts ADD COLUMN is_default boolean NOT NULL DEFAULT false;
UPDATE einvoice_accounts SET is_default = true;
ALTER TABLE einvoice_accounts DROP CONSTRAINT einvoice_accounts_pkey;
ALTER TABLE einvoice_accounts ADD PRIMARY KEY (tenant_id, environment, provider);
CREATE UNIQUE INDEX one_default_einvoice_account ON einvoice_accounts (tenant_id, environment) WHERE is_default;

CREATE TABLE einvoice_routing (
  tenant_id    uuid NOT NULL,
  environment  text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  country      char(2) NOT NULL,
  account      text NOT NULL,
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, environment, country),
  FOREIGN KEY (tenant_id, environment, account) REFERENCES einvoice_accounts (tenant_id, environment, provider) ON DELETE CASCADE
);
GRANT SELECT, INSERT, UPDATE, DELETE ON einvoice_routing TO payments_system;

-- Two accounts number their documents on their own: a legal number is unique within an account, as it always was within the only account there was. (A seller who
-- runs two accounts keeps their series apart with the integrators; Payrudder cannot see across them.)
DROP INDEX einvoice_legal_number_once;
CREATE UNIQUE INDEX einvoice_legal_number_once ON einvoice_issuances (tenant_id, environment, provider, legal_number) WHERE legal_number IS NOT NULL;

ALTER TABLE provider_account_events ADD COLUMN kind text NOT NULL DEFAULT 'payment' CHECK (kind IN ('payment', 'einvoice'));
ALTER TABLE provider_account_events DROP CONSTRAINT provider_account_events_action_check;
ALTER TABLE provider_account_events ADD CONSTRAINT provider_account_events_action_check
  CHECK (action IN ('connected', 'replaced', 'removed', 'default_set', 'routing_changed'));

-- +goose Down
-- Going back fails if two accounts handed out the same number, which is what the narrower index would have refused.
DROP INDEX einvoice_legal_number_once;
CREATE UNIQUE INDEX einvoice_legal_number_once ON einvoice_issuances (tenant_id, environment, legal_number) WHERE legal_number IS NOT NULL;
ALTER TABLE provider_account_events DROP CONSTRAINT provider_account_events_action_check;
DELETE FROM provider_account_events WHERE action IN ('default_set', 'routing_changed') OR kind <> 'payment';
ALTER TABLE provider_account_events ADD CONSTRAINT provider_account_events_action_check CHECK (action IN ('connected', 'removed', 'replaced'));
ALTER TABLE provider_account_events DROP COLUMN kind;
DROP TABLE einvoice_routing;
DROP INDEX one_default_einvoice_account;
DELETE FROM einvoice_accounts WHERE NOT is_default;
ALTER TABLE einvoice_accounts DROP CONSTRAINT einvoice_accounts_pkey;
ALTER TABLE einvoice_accounts ADD PRIMARY KEY (tenant_id, environment);
ALTER TABLE einvoice_accounts DROP COLUMN is_default;
