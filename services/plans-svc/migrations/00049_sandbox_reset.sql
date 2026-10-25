-- Phase 45 E: resetting a sandbox. plans-svc owns the job; each service deletes its own part (SandboxResetService). Here: the job table, and the right of plans_system to
-- delete what a reset deletes from plans_db (the customers' side: customers, subscriptions and their history, webhook history), nothing of the configuration.

-- +goose Up
CREATE TABLE sandbox_resets (
  id            uuid PRIMARY KEY,
  app_id        uuid NOT NULL REFERENCES apps (id),
  tenant_id     uuid NOT NULL REFERENCES tenants (id),
  requested_by  uuid REFERENCES users (id),            -- NULL: the retention sweep asked
  kind          text NOT NULL CHECK (kind IN ('manual', 'retention')),
  state         text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'done', 'failed')),
  steps         jsonb NOT NULL DEFAULT '[]',            -- [{name, state: pending|done|failed|skipped, deleted: {table: rows}, error}]
  error         text NOT NULL DEFAULT '',
  lease_until   timestamptz,                            -- while running: another replica takes the job over after this
  created_at    timestamptz NOT NULL DEFAULT now(),
  started_at    timestamptz,
  finished_at   timestamptz
);
-- One open reset per sandbox: asking again while one runs is answered with that one.
CREATE UNIQUE INDEX one_open_sandbox_reset ON sandbox_resets (tenant_id) WHERE state IN ('queued', 'running');
CREATE INDEX sandbox_resets_by_tenant ON sandbox_resets (tenant_id, created_at DESC);

-- +goose StatementBegin
CREATE FUNCTION sandbox_resets_only_sandbox() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  t tenants;
BEGIN
  SELECT * INTO t FROM tenants WHERE id = NEW.tenant_id;
  IF t.environment <> 'sandbox' OR t.app_id <> NEW.app_id THEN
    RAISE EXCEPTION 'only the sandbox of an app can be reset' USING ERRCODE = 'P0001', HINT = 'sandbox_only';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER sandbox_resets_sandbox_only BEFORE INSERT ON sandbox_resets FOR EACH ROW EXECUTE FUNCTION sandbox_resets_only_sandbox();

-- No row level security on purpose: only the system role touches the table, which names the tenant itself (listed in the RLS allowlist of the integration test).
GRANT SELECT, INSERT, UPDATE ON sandbox_resets TO plans_system;

GRANT DELETE ON customer_coupons, coupon_redemptions, subscription_adjustments, subscription_changes, subscription_coupons, provider_migrations, subscriptions, customers,
  webhook_deliveries, webhook_events, outbox, affiliate_commissions, affiliate_reversals, affiliate_attributions, affiliate_attribution_rejections,
  affiliate_clicks_daily TO plans_system;
GRANT SELECT ON customer_coupons, coupon_redemptions, webhook_deliveries, webhook_events TO plans_system;
-- A coupon's count of redemptions starts at zero again with the ledger it counts.
GRANT UPDATE (redeemed) ON coupons, promotion_codes TO plans_system;

-- An attribution is set once and never changes (00047). A reset of a sandbox deletes the customers it names, so a DELETE is allowed there, for a sandbox tenant
-- only: UPDATE stays refused everywhere, and so does any DELETE in a live tenant.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION affiliate_attributions_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND EXISTS (SELECT 1 FROM tenants WHERE id = OLD.tenant_id AND environment = 'sandbox') THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'an affiliate attribution is set once and never changes' USING ERRCODE = 'P0001', HINT = 'attribution_immutable';
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION affiliate_attributions_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'an affiliate attribution is set once and never changes' USING ERRCODE = 'P0001', HINT = 'attribution_immutable';
END $$;
-- +goose StatementEnd
REVOKE UPDATE (redeemed) ON coupons, promotion_codes FROM plans_system;
REVOKE DELETE ON customer_coupons, coupon_redemptions, subscription_adjustments, subscription_changes, subscription_coupons, provider_migrations, subscriptions, customers,
  webhook_deliveries, webhook_events, affiliate_commissions, affiliate_reversals, affiliate_attributions, affiliate_attribution_rejections, affiliate_clicks_daily FROM plans_system;
DROP TABLE sandbox_resets;
DROP FUNCTION sandbox_resets_only_sandbox();
