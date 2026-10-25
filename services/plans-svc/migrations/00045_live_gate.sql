-- Phase 45 A+B: live is earned, and keys carry more than a secret.
--
-- A live tenant is created `inactive` and has no keys until the go-live checklist is complete (owner e-mail verified, legal profile, a live payment provider,
-- a published plan, terms accepted). Every tenant that exists today keeps working: it is `active`. A sandbox tenant is always active.
-- An API key gets a scope (full, ingest-only, read-only), a last-used time and an end time (the old key of a rotation stays valid for an overlap window).

-- +goose Up
ALTER TABLE tenants
  ADD COLUMN status            text NOT NULL DEFAULT 'active' CHECK (status IN ('inactive', 'active', 'suspended')),
  ADD COLUMN activated_at      timestamptz,
  ADD COLUMN terms_accepted_at timestamptz,
  ADD COLUMN terms_accepted_by uuid REFERENCES users (id),
  ADD CONSTRAINT sandbox_is_always_active CHECK (environment = 'live' OR status = 'active');
UPDATE tenants SET activated_at = created_at WHERE environment = 'live';
-- From here on the status is always chosen by the code that creates a tenant.
ALTER TABLE tenants ALTER COLUMN status DROP DEFAULT;

ALTER TABLE api_keys
  ADD COLUMN scope        text NOT NULL DEFAULT 'full' CHECK (scope IN ('full', 'ingest', 'read')),
  ADD COLUMN last_used_at timestamptz,
  ADD COLUMN expires_at   timestamptz;

-- The last line of defence: a key whose environment disagrees with its tenant's, or a live key of a tenant that is not active, cannot be written whatever the
-- code above did.
-- +goose StatementBegin
CREATE FUNCTION api_keys_check_tenant() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  t tenants;
BEGIN
  SELECT * INTO t FROM tenants WHERE id = NEW.tenant_id;
  IF (t.environment = 'sandbox') <> (NEW.env = 'test') THEN
    RAISE EXCEPTION 'an api key environment must match its tenant' USING ERRCODE = 'P0001', HINT = 'key_environment';
  END IF;
  IF t.environment = 'live' AND t.status <> 'active' AND NEW.status = 'active' THEN
    RAISE EXCEPTION 'a live tenant needs to be active to have keys' USING ERRCODE = 'P0001', HINT = 'tenant_inactive';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER api_keys_tenant_check BEFORE INSERT ON api_keys FOR EACH ROW EXECUTE FUNCTION api_keys_check_tenant();

-- Live webhook endpoints follow the same rule. The trigger reads the tenant through current_tenant() because the app role cannot read the table.
-- +goose StatementBegin
CREATE FUNCTION webhook_endpoints_check_tenant() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  t tenants;
BEGIN
  IF NEW.active THEN
    SELECT * INTO t FROM current_tenant();
    IF t.environment = 'live' AND t.status <> 'active' THEN
      RAISE EXCEPTION 'a live tenant needs to be active to have webhook endpoints' USING ERRCODE = 'P0001', HINT = 'tenant_inactive';
    END IF;
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER webhook_endpoints_tenant_check BEFORE INSERT OR UPDATE OF active ON webhook_endpoints
  FOR EACH ROW EXECUTE FUNCTION webhook_endpoints_check_tenant();

-- +goose Down
DROP TRIGGER webhook_endpoints_tenant_check ON webhook_endpoints;
DROP FUNCTION webhook_endpoints_check_tenant();
DROP TRIGGER api_keys_tenant_check ON api_keys;
DROP FUNCTION api_keys_check_tenant();
ALTER TABLE api_keys DROP COLUMN expires_at, DROP COLUMN last_used_at, DROP COLUMN scope;
ALTER TABLE tenants DROP CONSTRAINT sandbox_is_always_active;
ALTER TABLE tenants DROP COLUMN terms_accepted_by, DROP COLUMN terms_accepted_at, DROP COLUMN activated_at, DROP COLUMN status;
