-- Phase 20: plan and interval changes (docs/15-billing-lifecycle.md). A change that waits for the end of the paid period is a pending
-- row here until billing's tick applies it; a change that moved money leaves an adjustment for billing-svc to invoice or credit.

-- +goose Up
CREATE TABLE subscription_changes (
  tenant_id           uuid NOT NULL,
  id                  uuid NOT NULL,
  subscription_id     uuid NOT NULL,
  plan_version_id     uuid NOT NULL,
  interval            text NOT NULL CHECK (interval IN ('month', 'year')),
  effective_at        timestamptz NOT NULL,
  state               text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'applied', 'cancelled')),
  created_at          timestamptz NOT NULL DEFAULT now(),
  applied_at          timestamptz,
  new_subscription_id uuid,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, subscription_id) REFERENCES subscriptions (tenant_id, id),
  FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions (tenant_id, id)
);
CREATE UNIQUE INDEX one_pending_change ON subscription_changes (tenant_id, subscription_id) WHERE state = 'pending';
CREATE INDEX subscription_changes_due ON subscription_changes (effective_at) WHERE state = 'pending';

-- Money a change moved: the unused, prepaid part of the old fee (credit) and the new plan's fee from the change (charge). billing_billed_at
-- is set once billing-svc has turned the net into an invoice or a credit balance.
CREATE TABLE subscription_adjustments (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  subscription_id  uuid NOT NULL,   -- the subscription the change created
  old_plan_code    text NOT NULL,
  from_at          timestamptz NOT NULL,
  to_at            timestamptz NOT NULL,
  credit_minor     bigint NOT NULL CHECK (credit_minor >= 0),
  charge_minor     bigint NOT NULL CHECK (charge_minor >= 0),
  created_at       timestamptz NOT NULL DEFAULT now(),
  billed_at        timestamptz,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, subscription_id) REFERENCES subscriptions (tenant_id, id)
);
CREATE INDEX subscription_adjustments_unbilled ON subscription_adjustments (created_at) WHERE billed_at IS NULL;

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['subscription_changes', 'subscription_adjustments'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE ON subscription_changes, subscription_adjustments TO plans_app;
GRANT SELECT ON subscription_changes, subscription_adjustments TO plans_system;

-- +goose Down
DROP TABLE subscription_adjustments, subscription_changes;
