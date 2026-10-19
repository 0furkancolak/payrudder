-- Phase 13: customers carry a country and billing currency, plans can have a fixed fee per currency, and
-- subscriptions get a lifecycle (docs/14-billing-platform.md). Existing rows keep working: every new column has a default.

-- +goose Up
ALTER TABLE customers
  ADD COLUMN country           char(2)  CHECK (country ~ '^[A-Z]{2}$'),
  ADD COLUMN billing_currency  char(3)  CHECK (billing_currency ~ '^[A-Z]{3}$');

-- Fixed fee in a currency other than the tenant's. The tenant currency's fee stays in plan_versions.base_fee_minor.
CREATE TABLE plan_version_fees (
  tenant_id        uuid NOT NULL,
  plan_version_id  uuid NOT NULL,
  currency         char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  base_fee_minor   bigint  NOT NULL CHECK (base_fee_minor >= 0),
  PRIMARY KEY (tenant_id, plan_version_id, currency),
  FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions (tenant_id, id)
);

ALTER TABLE subscriptions
  ADD COLUMN state                text NOT NULL DEFAULT 'active'
    CHECK (state IN ('trialing', 'active', 'past_due', 'paused', 'cancelled')),
  ADD COLUMN interval             text NOT NULL DEFAULT 'month' CHECK (interval IN ('month', 'year')),
  ADD COLUMN currency             char(3),
  ADD COLUMN trial_ends_at        timestamptz,
  ADD COLUMN cancel_at_period_end boolean NOT NULL DEFAULT false,
  ADD COLUMN paused_at            timestamptz,
  ADD COLUMN billing_anchor       timestamptz,    -- start of the calendar month (tenant time zone) the subscription began in; a plan change keeps it
  ADD COLUMN changed_from_id      uuid;           -- the subscription this one replaced in a plan change

-- Every existing subscription is in its tenant's currency.
UPDATE subscriptions s SET currency = t.currency FROM tenants t WHERE t.id = s.tenant_id;
-- A subscription that already ended is cancelled.
UPDATE subscriptions SET state = 'cancelled' WHERE NOT upper_inf(active);
UPDATE subscriptions SET billing_anchor = lower(active);
ALTER TABLE subscriptions ALTER COLUMN currency SET NOT NULL, ALTER COLUMN billing_anchor SET NOT NULL;

-- Usage prices in a currency other than the tenant's: the same shape as plan_prices, one set per currency. A plan version sold in
-- another currency prices every metered component in it.
CREATE TABLE plan_currency_prices (
  tenant_id        uuid NOT NULL,
  plan_version_id  uuid NOT NULL,
  currency         char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  meter_code       text NOT NULL,
  model            text NOT NULL CHECK (model IN ('PER_UNIT', 'TIERED_GRADUATED', 'PACKAGE')),
  config           jsonb NOT NULL,
  PRIMARY KEY (tenant_id, plan_version_id, currency, meter_code),
  FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions (tenant_id, id)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['plan_version_fees', 'plan_currency_prices'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT ON plan_version_fees, plan_currency_prices TO plans_app;
GRANT SELECT ON plan_version_fees, plan_currency_prices TO plans_system;

-- +goose Down
DROP TABLE plan_currency_prices, plan_version_fees;
ALTER TABLE subscriptions
  DROP COLUMN changed_from_id, DROP COLUMN billing_anchor, DROP COLUMN paused_at, DROP COLUMN cancel_at_period_end, DROP COLUMN trial_ends_at,
  DROP COLUMN currency, DROP COLUMN interval, DROP COLUMN state;
ALTER TABLE customers DROP COLUMN billing_currency, DROP COLUMN country;
