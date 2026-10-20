-- Phase 25: an account has apps, an app has a sandbox and a live tenant. The tenant stays the unit of isolation (RLS); the app groups the two environments of one
-- application, so a company can run several applications under one account, each with its own keys, customers, plans and invoices.

-- +goose Up
CREATE TABLE apps (
  id          uuid PRIMARY KEY,
  account_id  uuid NOT NULL REFERENCES accounts (id),
  name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  slug        text NOT NULL CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 60),
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (account_id, slug)
);

ALTER TABLE tenants ADD COLUMN app_id uuid REFERENCES apps (id);
INSERT INTO apps (id, account_id, name, slug) SELECT gen_random_uuid(), id, name, 'default' FROM accounts;
UPDATE tenants t SET app_id = a.id FROM apps a WHERE a.account_id = t.account_id;
ALTER TABLE tenants ALTER COLUMN app_id SET NOT NULL;
ALTER TABLE tenants DROP CONSTRAINT tenants_account_id_environment_key;
ALTER TABLE tenants ADD CONSTRAINT tenants_app_id_environment_key UNIQUE (app_id, environment);
CREATE INDEX tenants_by_account ON tenants (account_id);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tenants_immutable_settings() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.currency IS DISTINCT FROM OLD.currency OR NEW.timezone IS DISTINCT FROM OLD.timezone
     OR NEW.environment IS DISTINCT FROM OLD.environment OR NEW.account_id IS DISTINCT FROM OLD.account_id
     OR NEW.app_id IS DISTINCT FROM OLD.app_id THEN
    RAISE EXCEPTION 'tenant currency, timezone, environment, account and app are immutable'
      USING ERRCODE = 'P0001', HINT = 'tenant_immutable';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT ON apps TO plans_system;

-- +goose Down
ALTER TABLE tenants DROP CONSTRAINT tenants_app_id_environment_key;
ALTER TABLE tenants ADD CONSTRAINT tenants_account_id_environment_key UNIQUE (account_id, environment);
DROP INDEX tenants_by_account;
ALTER TABLE tenants DROP COLUMN app_id;
DROP TABLE apps;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tenants_immutable_settings() RETURNS trigger
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
