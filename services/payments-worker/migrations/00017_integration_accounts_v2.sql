-- Step 1 of the account_id migration (ADR 0026, docs/runbooks/account-id-migration-rehearsal.md): the new shape of an account beside the old. Nothing reads these tables
-- yet except the shadow comparison (PAYMENTS_ACCOUNT_SHADOW, off), and no old table is touched: provider_accounts, einvoice_accounts, einvoice_routing and every table
-- of the payment path stay as they are.
--
-- An account is a row with an id of its own; who may use it is a binding. Every account created by the backfill is owned by its tenant (owner_kind 'tenant'); the
-- 'organisation' owner is the last step of the migration and nothing writes it. The sealed settings are bound to the account id, never to a tenant or a key
-- ("integration-account:<id>"), so a box copied to another row does not open and a shared account would not name an app.
--
-- DEFAULTS (not agreed): the environment is a column of the account (a sandbox and a live account are two accounts, as the two old rows were); a payment account and an
-- e-invoice account are told apart by `kind`; `label` is '' for the primary account (the key "paddle" is provider paddle, label ''; "paddle~eu" is provider paddle,
-- label eu). Like the other tables of payments_db these hold ids and sealed settings only and have no row-level security (only payments_system touches them).

-- +goose Up
CREATE TABLE integration_accounts (
  id            uuid PRIMARY KEY,
  owner_kind    text NOT NULL CHECK (owner_kind IN ('tenant', 'organisation')),
  owner_id      uuid NOT NULL,
  kind          text NOT NULL CHECK (kind IN ('payment', 'einvoice')),
  environment   text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  provider      text NOT NULL,
  label         text NOT NULL DEFAULT '',
  mode          text CHECK (mode IN ('test', 'production')),
  settings_enc  bytea NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (owner_kind, owner_id, kind, environment, provider, label)
);

CREATE TABLE integration_bindings (
  tenant_id    uuid NOT NULL,
  environment  text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  kind         text NOT NULL CHECK (kind IN ('payment', 'einvoice')),
  account_id   uuid NOT NULL REFERENCES integration_accounts (id),
  provider     text NOT NULL,
  label        text NOT NULL DEFAULT '',
  is_default   boolean NOT NULL DEFAULT false,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, environment, kind, account_id),
  UNIQUE (tenant_id, environment, kind, provider, label)
);
CREATE UNIQUE INDEX one_default_integration_binding ON integration_bindings (tenant_id, environment, kind) WHERE is_default;

GRANT SELECT, INSERT, UPDATE, DELETE ON integration_accounts, integration_bindings TO payments_system;

-- +goose Down
DROP TABLE integration_bindings, integration_accounts;
