-- Phase 45 D: the audit of promoting an app's sandbox configuration to its live environment. One row per applied promotion (a dry run writes nothing).
-- Promotion goes one way only, and the database says so: the source must be the app's sandbox tenant and the target its live tenant.

-- +goose Up
CREATE TABLE config_promotions (
  id                uuid PRIMARY KEY,
  app_id            uuid NOT NULL REFERENCES apps (id),
  user_id           uuid NOT NULL REFERENCES users (id),
  sandbox_tenant_id uuid NOT NULL REFERENCES tenants (id),
  live_tenant_id    uuid NOT NULL REFERENCES tenants (id),
  created           int NOT NULL CHECK (created >= 0),
  unchanged         int NOT NULL CHECK (unchanged >= 0),
  conflicts         int NOT NULL CHECK (conflicts >= 0),
  skipped           int NOT NULL CHECK (skipped >= 0),
  failed            int NOT NULL CHECK (failed >= 0),
  detail            jsonb NOT NULL,                    -- the changes, without secrets
  created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX config_promotions_by_app ON config_promotions (app_id, created_at DESC);

-- +goose StatementBegin
CREATE FUNCTION config_promotions_one_way() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  s tenants;
  l tenants;
BEGIN
  SELECT * INTO s FROM tenants WHERE id = NEW.sandbox_tenant_id;
  SELECT * INTO l FROM tenants WHERE id = NEW.live_tenant_id;
  IF s.environment <> 'sandbox' OR l.environment <> 'live' OR s.app_id <> NEW.app_id OR l.app_id <> NEW.app_id THEN
    RAISE EXCEPTION 'configuration is promoted from the sandbox to the live environment of one app, and only that way' USING ERRCODE = 'P0001', HINT = 'promotion_direction';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER config_promotions_direction BEFORE INSERT ON config_promotions FOR EACH ROW EXECUTE FUNCTION config_promotions_one_way();

GRANT SELECT, INSERT ON config_promotions TO plans_system;

-- +goose Down
DROP TABLE config_promotions;
DROP FUNCTION config_promotions_one_way();
