-- plans_db schema (docs/05-data-model.md#plans_db).
-- Roles: plans_migrator owns everything; plans_app (request handlers, RLS enforced); plans_system (BYPASSRLS).

-- +goose Up
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Tenant of the current transaction, set by pgxx.InTenantTx. NULLIF: a pooled connection that saw a transaction-local
-- set_config returns '' afterwards, not NULL.
CREATE FUNCTION app_tenant_id() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE
AS $$ SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid $$;

CREATE TABLE accounts (
  id          uuid PRIMARY KEY,
  name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tenants (
  id            uuid PRIMARY KEY,
  account_id    uuid NOT NULL REFERENCES accounts (id),
  environment   text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  name          text NOT NULL,
  tier          text NOT NULL CHECK (tier IN ('free', 'standard', 'enterprise')),
  currency      char(3) NOT NULL,
  timezone      text NOT NULL,
  grace_period  interval NOT NULL CHECK (grace_period BETWEEN interval '0' AND interval '7 days'),
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (account_id, environment)
);

-- +goose StatementBegin
CREATE FUNCTION tenants_immutable_settings() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.currency IS DISTINCT FROM OLD.currency OR NEW.timezone IS DISTINCT FROM OLD.timezone
     OR NEW.environment IS DISTINCT FROM OLD.environment OR NEW.account_id IS DISTINCT FROM OLD.account_id THEN
    RAISE EXCEPTION 'tenant currency, timezone, environment and account are immutable'
      USING ERRCODE = 'P0001', HINT = 'tenant_immutable';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER tenants_immutable BEFORE UPDATE ON tenants
  FOR EACH ROW EXECUTE FUNCTION tenants_immutable_settings();

CREATE TABLE api_keys (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants (id),
  prefix      char(8) NOT NULL UNIQUE,
  key_hash    bytea NOT NULL CHECK (length(key_hash) = 32),
  env         text NOT NULL CHECK (env IN ('live', 'test')),
  status      text NOT NULL CHECK (status IN ('active', 'revoked')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  revoked_at  timestamptz
);
CREATE INDEX api_keys_tenant ON api_keys (tenant_id);

CREATE TABLE customers (
  tenant_id       uuid NOT NULL,
  id              uuid NOT NULL,
  external_id     text NOT NULL CHECK (length(external_id) BETWEEN 1 AND 128),
  name            text NOT NULL,
  email           text NOT NULL,
  payment_method  text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, external_id)
);

CREATE TABLE meters (
  tenant_id       uuid NOT NULL,
  code            text NOT NULL CHECK (code ~ '^[a-z][a-z0-9_]{0,62}$'),
  version         int  NOT NULL CHECK (version > 0),
  aggregation     text NOT NULL CHECK (aggregation IN ('SUM', 'COUNT', 'MAX')),
  value_property  text,
  filter          jsonb NOT NULL DEFAULT '{}',
  quantity_scale  smallint NOT NULL CHECK (quantity_scale BETWEEN 0 AND 6),
  unit_label      text NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, code, version),
  CHECK ((aggregation = 'COUNT') = (value_property IS NULL))
);

CREATE TABLE plans (
  tenant_id   uuid NOT NULL,
  id          uuid NOT NULL,
  code        text NOT NULL CHECK (code ~ '^[a-z][a-z0-9_-]{0,62}$'),
  name        text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, code)
);

CREATE TABLE plan_versions (
  tenant_id       uuid NOT NULL,
  id              uuid NOT NULL,
  plan_id         uuid NOT NULL,
  version         int  NOT NULL CHECK (version > 0),
  base_fee_minor  bigint NOT NULL DEFAULT 0 CHECK (base_fee_minor >= 0),
  published_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, plan_id, version),
  FOREIGN KEY (tenant_id, plan_id) REFERENCES plans (tenant_id, id)
);

CREATE TABLE plan_prices (
  tenant_id        uuid NOT NULL,
  plan_version_id  uuid NOT NULL,
  meter_code       text NOT NULL,
  model            text NOT NULL CHECK (model IN ('PER_UNIT', 'TIERED_GRADUATED', 'PACKAGE')),
  config           jsonb NOT NULL,   -- validated in Go (internal/domain); quantities stored as scaled units
  PRIMARY KEY (tenant_id, plan_version_id, meter_code),
  FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions (tenant_id, id)
);

CREATE TABLE plan_entitlements (
  tenant_id             uuid NOT NULL,
  plan_version_id       uuid NOT NULL,
  meter_code            text NOT NULL,
  limit_units           bigint NOT NULL CHECK (limit_units >= 0),   -- scaled units; 0 = unlimited
  alert_thresholds_pct  int[]  NOT NULL DEFAULT '{80,100}',
  PRIMARY KEY (tenant_id, plan_version_id, meter_code),
  FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions (tenant_id, id)
);

CREATE TABLE subscriptions (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  customer_id      uuid NOT NULL,
  plan_version_id  uuid NOT NULL,
  active           tstzrange NOT NULL CHECK (NOT isempty(active) AND lower(active) IS NOT NULL),
  created_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
  FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions (tenant_id, id),
  CONSTRAINT one_active_subscription
    EXCLUDE USING gist (tenant_id WITH =, customer_id WITH =, active WITH &&)
);

CREATE TABLE outbox (                -- relay added in Phase 04 (plans.entitlements.v1)
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id      uuid NOT NULL,
  topic          text NOT NULL,
  partition_key  text NOT NULL,
  payload        bytea,
  headers        jsonb NOT NULL DEFAULT '{}',
  created_at     timestamptz NOT NULL DEFAULT now()
);

-- Row level security on every tenant-owned table.
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['customers', 'meters', 'plans', 'plan_versions', 'plan_prices', 'plan_entitlements', 'subscriptions'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- The app role reads its own tenant row through this function only (tenants itself is operator-only).
CREATE FUNCTION current_tenant() RETURNS SETOF tenants
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
AS $$ SELECT * FROM tenants WHERE id = app_tenant_id() $$;
REVOKE ALL ON FUNCTION current_tenant() FROM PUBLIC;

-- Grants. Append-only tables (meters, plans, plan versions and their prices/entitlements) get no UPDATE/DELETE.
GRANT SELECT, INSERT, UPDATE ON customers, subscriptions TO plans_app;
GRANT SELECT, INSERT ON meters, plans, plan_versions, plan_prices, plan_entitlements TO plans_app;
GRANT INSERT ON outbox TO plans_app;
GRANT EXECUTE ON FUNCTION current_tenant(), app_tenant_id() TO plans_app;

GRANT SELECT, INSERT, UPDATE ON accounts, tenants, api_keys TO plans_system;
GRANT SELECT ON customers, meters, plans, plan_versions, plan_prices, plan_entitlements, subscriptions TO plans_system;
GRANT SELECT, DELETE ON outbox TO plans_system;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO plans_system;

-- +goose Down
DROP TABLE outbox, subscriptions, plan_entitlements, plan_prices, plan_versions, plans, meters, customers, api_keys;
DROP FUNCTION current_tenant();
DROP TABLE tenants, accounts;
DROP FUNCTION tenants_immutable_settings(), app_tenant_id();
