-- Phase 16: moving a subscription from one payment provider to another (docs/14-billing-platform.md#provider-migration).
-- +goose Up
CREATE TABLE provider_migrations (
  tenant_id           uuid NOT NULL,
  id                  uuid NOT NULL,
  subscription_id     uuid NOT NULL,
  new_subscription_id uuid,
  from_provider       text NOT NULL DEFAULT '',
  to_provider         text NOT NULL CHECK (to_provider ~ '^[a-z][a-z0-9_-]{0,31}$'),
  state               text NOT NULL CHECK (state IN ('started', 'completed', 'aborted')),
  cutover_at          timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, subscription_id) REFERENCES subscriptions (tenant_id, id)
);
-- One migration under way per subscription.
CREATE UNIQUE INDEX one_started_migration ON provider_migrations (tenant_id, subscription_id) WHERE state = 'started';
ALTER TABLE provider_migrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE provider_migrations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON provider_migrations USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, UPDATE ON provider_migrations TO plans_app;
GRANT SELECT ON provider_migrations TO plans_system;

-- +goose Down
DROP TABLE provider_migrations;
