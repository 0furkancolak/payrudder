-- Phase 22: coupons. A coupon takes a percentage or an amount off the fixed fee of a subscription Quotify renews, once, for N periods or for as long as the
-- subscription lasts. One coupon per subscription; a plan change carries it over.

-- +goose Up
CREATE TABLE coupons (
  tenant_id        uuid NOT NULL,
  code             text NOT NULL CHECK (code ~ '^[A-Za-z0-9_-]{2,40}$'),
  name             text NOT NULL DEFAULT '',
  percent_bps      int  CHECK (percent_bps BETWEEN 1 AND 10000),   -- 1000 = 10 %
  amount_minor     bigint CHECK (amount_minor > 0),
  currency         char(3) CHECK (currency ~ '^[A-Z]{3}$'),
  duration         text NOT NULL CHECK (duration IN ('once', 'repeating', 'forever')),
  duration_periods int  CHECK (duration_periods >= 1),
  max_redemptions  int  CHECK (max_redemptions >= 1),
  redeemed         int  NOT NULL DEFAULT 0 CHECK (redeemed >= 0),
  expires_at       timestamptz,
  active           boolean NOT NULL DEFAULT true,
  created_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, code),
  CONSTRAINT coupon_is_percent_or_amount CHECK ((percent_bps IS NOT NULL) <> (amount_minor IS NOT NULL)),
  CONSTRAINT amount_coupon_has_currency CHECK (amount_minor IS NULL OR currency IS NOT NULL),
  CONSTRAINT repeating_has_periods CHECK ((duration = 'repeating') = (duration_periods IS NOT NULL))
);

CREATE TABLE subscription_coupons (
  tenant_id       uuid NOT NULL,
  subscription_id uuid NOT NULL,
  coupon_code     text NOT NULL,
  redeemed_at     timestamptz NOT NULL DEFAULT now(),
  periods_applied int  NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_id, subscription_id),
  FOREIGN KEY (tenant_id, subscription_id) REFERENCES subscriptions (tenant_id, id),
  FOREIGN KEY (tenant_id, coupon_code) REFERENCES coupons (tenant_id, code)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['coupons', 'subscription_coupons'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE ON coupons, subscription_coupons TO plans_app;
GRANT DELETE ON subscription_coupons TO plans_app;
GRANT SELECT ON coupons, subscription_coupons TO plans_system;

-- +goose Down
DROP TABLE subscription_coupons, coupons;
