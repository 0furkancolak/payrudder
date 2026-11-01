-- Plan 15: a price change is a record of its own (the twin of ../00006_price_changes.sql). SchedulePriceChange moves the subscriptions of one plan version to another at their next renewal; this table is the
-- run (who asked, with what notice, the earliest date a price changes, open or withdrawn) and price_change_items is what happened to each subscription: scheduled
-- (change_id, the subscription_changes row that carries it), or skipped with a reason. Whether a scheduled item is still waiting, applied or withdrawn is the state of
-- its subscription_changes row, so nothing here has to be kept in step with it.
-- Only one price change per plan and pair of versions is open at a time: running it again continues that one (the skipped subscriptions are tried again).

-- +goose Up
CREATE TABLE price_changes (
  tenant_id     uuid NOT NULL,
  id            uuid NOT NULL,
  plan_code     text NOT NULL,
  from_version  integer NOT NULL CHECK (from_version >= 1),
  to_version    integer NOT NULL CHECK (to_version >= 1 AND to_version <> from_version),
  notice_days   integer NOT NULL CHECK (notice_days BETWEEN 14 AND 365),
  -- The earliest date a customer's price changes: the first scheduled renewal; until one is scheduled, the end of the notice.
  effective_at  timestamptz NOT NULL,
  state         text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'withdrawn')),
  created_by    text NOT NULL DEFAULT '' CHECK (length(created_by) <= 120),
  created_at    timestamptz NOT NULL DEFAULT now(),
  withdrawn_at  timestamptz,
  withdrawn_by  text NOT NULL DEFAULT '' CHECK (length(withdrawn_by) <= 120),
  PRIMARY KEY (tenant_id, id),
  CONSTRAINT price_changes_withdrawn_has_time CHECK ((state = 'withdrawn') = (withdrawn_at IS NOT NULL))
);
CREATE UNIQUE INDEX one_open_price_change ON price_changes (tenant_id, plan_code, from_version, to_version) WHERE state = 'open';
CREATE INDEX price_changes_recent ON price_changes (tenant_id, created_at DESC, id DESC);

CREATE TABLE price_change_items (
  tenant_id        uuid NOT NULL,
  price_change_id  uuid NOT NULL,
  subscription_id  uuid NOT NULL,
  change_id        uuid,
  -- Why a subscription was skipped (empty when it is scheduled): renews_soon, has_pending_change, run_by_provider, not_changeable or customer_blocked.
  reason           text NOT NULL DEFAULT '' CHECK (reason IN ('', 'renews_soon', 'has_pending_change', 'run_by_provider', 'not_changeable', 'customer_blocked')),
  -- The renewal the new price starts on, and when the customer's mail was queued.
  effective_at     timestamptz,
  notified_at      timestamptz,
  updated_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, price_change_id, subscription_id),
  FOREIGN KEY (tenant_id, price_change_id) REFERENCES price_changes (tenant_id, id),
  FOREIGN KEY (tenant_id, subscription_id) REFERENCES subscriptions (tenant_id, id),
  FOREIGN KEY (tenant_id, change_id) REFERENCES subscription_changes (tenant_id, id),
  CONSTRAINT price_change_items_skipped_or_scheduled CHECK ((change_id IS NULL) = (reason <> ''))
);
CREATE INDEX price_change_items_by_subscription ON price_change_items (tenant_id, subscription_id);

ALTER TABLE price_changes ENABLE ROW LEVEL SECURITY;
ALTER TABLE price_changes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON price_changes USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
ALTER TABLE price_change_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE price_change_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON price_change_items USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, UPDATE ON price_changes, price_change_items TO plans_app;
-- A sandbox reset deletes the subscriptions, and what refers to them first.
GRANT SELECT, DELETE ON price_changes, price_change_items TO plans_system;

-- +goose Down
DROP TABLE price_change_items;
DROP TABLE price_changes;
