-- payments_db, tenant schema: ../00017_integration_accounts_v2.sql (step 1 of the account_id migration, ADR 0026) for a schema. Nothing reads these tables but the shadow
-- comparison; no old table is touched. Like the other tables of payments_db they have no row-level security (only payments_system touches them).

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
