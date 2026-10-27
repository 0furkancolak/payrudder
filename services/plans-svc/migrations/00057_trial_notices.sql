-- A trialing subscription is reminded once, a few days before its trial ends (subscription.trial_will_end webhook event, and an e-mail to the customer). This row
-- is the "already reminded" marker. It is keyed by the customer and the end of the trial, not by the subscription: a plan change during a trial replaces the
-- subscription but keeps the trial end, and must not remind again; a trial that is extended or started afresh has another end and is reminded again.
-- The job inserts the row in the same transaction that queues the event and the mail, and ON CONFLICT DO NOTHING makes a second instance, or a second pass, a no-op.

-- +goose Up
CREATE TABLE subscription_trial_notices (
  tenant_id        uuid NOT NULL,
  customer_id      uuid NOT NULL,
  trial_ends_at    timestamptz NOT NULL,
  subscription_id  uuid NOT NULL,
  notified_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, customer_id, trial_ends_at),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id)
);
ALTER TABLE subscription_trial_notices ENABLE ROW LEVEL SECURITY;
ALTER TABLE subscription_trial_notices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON subscription_trial_notices USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT ON subscription_trial_notices TO plans_app;
-- plans_system finds the trials not yet reminded (across tenants) and empties a sandbox's notices when it is reset.
GRANT SELECT, DELETE ON subscription_trial_notices TO plans_system;

-- +goose Down
DROP TABLE subscription_trial_notices;
