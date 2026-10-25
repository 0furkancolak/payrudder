-- Phase 45 follow-up: the audit trail of what an owner does on the way to going live (the seller's legal and billing profile, the terms of service). One row per change;
-- nothing is ever updated or deleted. The provider connection is audited in payments-worker (provider_account_events), next to the credentials it is about.
-- The row names the app's live tenant and the database says so: the profile of a sandbox environment is not part of going live.

-- +goose Up
CREATE TABLE go_live_audit (
  id              uuid PRIMARY KEY,
  app_id          uuid NOT NULL REFERENCES apps (id),
  live_tenant_id  uuid NOT NULL REFERENCES tenants (id),
  user_id         uuid NOT NULL REFERENCES users (id),
  action          text NOT NULL CHECK (action IN ('billing_profile_saved', 'terms_accepted')),
  detail          jsonb NOT NULL DEFAULT '{}'::jsonb,   -- field names and the country, never the values of the profile
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX go_live_audit_by_app ON go_live_audit (app_id, created_at DESC);

-- +goose StatementBegin
CREATE FUNCTION go_live_audit_live_only() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  l tenants;
BEGIN
  SELECT * INTO l FROM tenants WHERE id = NEW.live_tenant_id;
  IF l.environment <> 'live' OR l.app_id <> NEW.app_id THEN
    RAISE EXCEPTION 'the go-live audit is about the live environment of the app' USING ERRCODE = 'P0001', HINT = 'go_live_audit_environment';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER go_live_audit_live BEFORE INSERT ON go_live_audit FOR EACH ROW EXECUTE FUNCTION go_live_audit_live_only();

GRANT SELECT, INSERT ON go_live_audit TO plans_system;

-- +goose Down
DROP TABLE go_live_audit;
DROP FUNCTION go_live_audit_live_only();
