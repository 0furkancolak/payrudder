-- Apps can be renamed and deleted. Deleting never erases: an app holds invoices, payments and subscriptions that accounting and the retention rules keep, so
-- a deleted app is archived. It leaves every list of the account, its API keys are revoked, no key can be made for it again and its slug is freed for a new
-- app; its tenants, customers, invoices, payments and usage stay where they are. Only the operator API still lists it (with its archive time).
-- Both actions leave a row in app_audit (nothing in it is ever updated or deleted).

-- +goose Up
ALTER TABLE apps
  ADD COLUMN archived_at timestamptz,
  ADD COLUMN archived_by uuid REFERENCES users (id),
  ADD CONSTRAINT apps_archived_by_needs_time CHECK (archived_by IS NULL OR archived_at IS NOT NULL);
CREATE INDEX apps_live_by_account ON apps (account_id) WHERE archived_at IS NULL;
-- Name and slug change on rename and archive; nothing else about an app does.
GRANT UPDATE (name, slug, archived_at, archived_by) ON apps TO plans_system;

CREATE TABLE app_audit (
  id          uuid PRIMARY KEY,
  app_id      uuid NOT NULL REFERENCES apps (id),
  user_id     uuid NOT NULL REFERENCES users (id),
  action      text NOT NULL CHECK (action IN ('renamed', 'archived')),
  detail      jsonb NOT NULL DEFAULT '{}'::jsonb,   -- renamed: the old and the new name; archived: how many keys were revoked
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX app_audit_by_app ON app_audit (app_id, created_at DESC);
GRANT SELECT, INSERT ON app_audit TO plans_system;

-- The last line of defence, as for a live tenant that is not active: an active key of an archived app cannot be written whatever the code above did.
-- +goose StatementBegin
CREATE FUNCTION api_keys_check_app_not_archived() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'active' AND EXISTS (
    SELECT 1 FROM tenants t JOIN apps a ON a.id = t.app_id WHERE t.id = NEW.tenant_id AND a.archived_at IS NOT NULL
  ) THEN
    RAISE EXCEPTION 'an archived app has no active keys' USING ERRCODE = 'P0001', HINT = 'app_archived';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER api_keys_app_not_archived BEFORE INSERT OR UPDATE OF status ON api_keys
  FOR EACH ROW EXECUTE FUNCTION api_keys_check_app_not_archived();

-- +goose Down
DROP TRIGGER api_keys_app_not_archived ON api_keys;
DROP FUNCTION api_keys_check_app_not_archived();
DROP TABLE app_audit;
REVOKE UPDATE (name, slug, archived_at, archived_by) ON apps FROM plans_system;
DROP INDEX apps_live_by_account;
ALTER TABLE apps DROP CONSTRAINT apps_archived_by_needs_time, DROP COLUMN archived_by, DROP COLUMN archived_at;
