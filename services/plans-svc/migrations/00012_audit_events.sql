-- Plan 22 (G4): the audit log. One append-only table per service database (pkg/audit writes it, in the transaction of the change it records): who (a user, an API
-- key, the operator, a job), what (action, target, a one-line summary with no secret in it, a small detail and the hashes of the state before and after), from where
-- (address, request id) and when. The app role and the system role may only add rows; a trigger refuses UPDATE and DELETE for everybody, the owner included. The
-- one DELETE it lets through is the copy of a tenant between the pooled database and a cell (payrudder.tenant_move, as for the financial tables).
-- tenant_id is NULL for an event that belongs to no tenant (a sign-in, a member of an account); account_id is set for an event that is an account's, not a tenant's.

-- +goose Up
CREATE TABLE audit_events (
  id          uuid PRIMARY KEY,
  tenant_id   uuid,
  account_id  uuid,
  actor_type  text NOT NULL CHECK (actor_type IN ('user', 'api_key', 'operator', 'system')),
  actor_id    text NOT NULL DEFAULT '' CHECK (length(actor_id) <= 120),
  actor_email text NOT NULL DEFAULT '' CHECK (length(actor_email) <= 254),
  action      text NOT NULL CHECK (length(action) <= 80 AND action ~ '^[a-z0-9_]+(\.[a-z0-9_]+)+$'),
  target_type text NOT NULL DEFAULT '' CHECK (length(target_type) <= 60),
  target_id   text NOT NULL DEFAULT '' CHECK (length(target_id) <= 200),
  summary     text NOT NULL CHECK (length(summary) <= 500),
  detail      jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(detail) = 'object' AND length(detail::text) <= 4096),
  before_hash text NOT NULL DEFAULT '' CHECK (length(before_hash) IN (0, 64)),
  after_hash  text NOT NULL DEFAULT '' CHECK (length(after_hash) IN (0, 64)),
  source_ip   text NOT NULL DEFAULT '' CHECK (length(source_ip) <= 45),
  request_id  text NOT NULL DEFAULT '' CHECK (length(request_id) <= 120),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_recent ON audit_events (created_at DESC, id DESC);
CREATE INDEX audit_events_by_tenant ON audit_events (tenant_id, created_at DESC, id DESC) WHERE tenant_id IS NOT NULL;
CREATE INDEX audit_events_by_account ON audit_events (account_id, created_at DESC, id DESC) WHERE account_id IS NOT NULL;
CREATE INDEX audit_events_by_action ON audit_events (action, created_at DESC, id DESC);

-- +goose StatementBegin
CREATE FUNCTION audit_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND current_setting('payrudder.tenant_move', true) = OLD.tenant_id::text THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'the audit log is append-only' USING ERRCODE = 'P0001', HINT = 'audit_events_append_only';
END $$;
-- +goose StatementEnd
CREATE TRIGGER audit_events_append_only BEFORE UPDATE OR DELETE ON audit_events FOR EACH ROW EXECUTE FUNCTION audit_events_append_only();

ALTER TABLE audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON audit_events USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT ON audit_events TO plans_app;
-- DELETE is for the copy of a tenant between databases only; the trigger refuses any other.
GRANT SELECT, INSERT, DELETE ON audit_events TO plans_system;

-- +goose Down
DROP TABLE audit_events;
DROP FUNCTION audit_events_append_only();
