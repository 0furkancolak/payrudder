-- Phase 46: coupons and promotion codes.
--
-- A coupon stays the definition of a discount (what it takes off). A promotion code is what a customer types: many codes point to one coupon, each with its own
-- cap, expiry and restrictions. Every redemption is a row of coupon_redemptions, claimed with a conditional update so two checkouts cannot both take the last
-- one. A coupon can now also be used for a credit purchase, a payment link or an invoice (not only the fixed fee of a subscription), and can give bonus credits.
-- customer_coupons holds a coupon waiting for a customer (a referral programme gives one to the customer it brought); the next eligible checkout without a
-- typed code uses it.
-- +goose Up
ALTER TABLE coupons
  ADD COLUMN id                uuid NOT NULL DEFAULT gen_random_uuid(),
  ADD COLUMN bonus_percent_bps int  CHECK (bonus_percent_bps BETWEEN 1 AND 100000),    -- extra credits as a share of the pack's: 1000 = 10 % more
  ADD COLUMN applies_to        text[] NOT NULL DEFAULT ARRAY['subscription', 'credit_purchase', 'payment']
    CHECK (cardinality(applies_to) >= 1 AND applies_to <@ ARRAY['subscription', 'credit_purchase', 'payment']);
ALTER TABLE coupons ADD CONSTRAINT coupons_id_key UNIQUE (tenant_id, id);
ALTER TABLE coupons DROP CONSTRAINT coupon_is_percent_or_amount;
ALTER TABLE coupons ADD CONSTRAINT coupon_is_percent_amount_or_bonus CHECK (num_nonnulls(percent_bps, amount_minor, bonus_percent_bps) = 1);
ALTER TABLE coupons ADD CONSTRAINT bonus_coupon_is_for_credits CHECK (bonus_percent_bps IS NULL OR applies_to = ARRAY['credit_purchase']);

CREATE TABLE promotion_codes (
  tenant_id           uuid NOT NULL,
  id                  uuid NOT NULL DEFAULT gen_random_uuid(),
  coupon_id           uuid NOT NULL,
  code                text NOT NULL CHECK (code ~ '^[A-Za-z0-9_-]{2,40}$'),
  active              boolean NOT NULL DEFAULT true,
  max_redemptions     int CHECK (max_redemptions >= 1),
  redeemed            int NOT NULL DEFAULT 0 CHECK (redeemed >= 0),
  expires_at          timestamptz,
  first_purchase_only boolean NOT NULL DEFAULT false,
  one_per_customer    boolean NOT NULL DEFAULT false,
  min_amount_minor    bigint CHECK (min_amount_minor > 0),
  min_amount_currency char(3) CHECK (min_amount_currency ~ '^[A-Z]{3}$'),
  allowed_plans       text[] NOT NULL DEFAULT '{}',          -- plan codes; empty: any plan
  allowed_packs       text[] NOT NULL DEFAULT '{}',          -- credit pack ids; empty: any pack
  allowed_currencies  text[] NOT NULL DEFAULT '{}',          -- empty: any currency
  restore_on_reversal boolean NOT NULL DEFAULT false,        -- a void or full refund gives the redemption back to the count
  created_at          timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, coupon_id) REFERENCES coupons (tenant_id, id),
  CONSTRAINT min_amount_has_currency CHECK ((min_amount_minor IS NULL) = (min_amount_currency IS NULL))
);
-- A customer types a code in any case; two codes that differ only by case would be one.
CREATE UNIQUE INDEX promotion_codes_code ON promotion_codes (tenant_id, upper(code));
CREATE INDEX promotion_codes_by_coupon ON promotion_codes (tenant_id, coupon_id, created_at DESC, id);

CREATE TABLE coupon_redemptions (
  tenant_id         uuid NOT NULL,
  id                uuid NOT NULL DEFAULT gen_random_uuid(),
  coupon_id         uuid NOT NULL,
  promotion_code_id uuid,
  customer_id       uuid,                                     -- NULL for a guest payment link
  target_kind       text NOT NULL CHECK (target_kind IN ('subscription', 'credit_purchase', 'payment', 'legacy')),
  target_id         uuid NOT NULL,                            -- the subscription, or the invoice of the purchase or payment
  invoice_id        uuid,
  discount_minor    bigint NOT NULL DEFAULT 0 CHECK (discount_minor >= 0),
  bonus_credits     bigint NOT NULL DEFAULT 0 CHECK (bonus_credits >= 0),
  currency          char(3),
  created_at        timestamptz NOT NULL DEFAULT now(),
  reversed_at       timestamptz,
  slot_returned     boolean NOT NULL DEFAULT false,           -- the reversal gave the redemption back to the count of coupon and code
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, coupon_id) REFERENCES coupons (tenant_id, id),
  FOREIGN KEY (tenant_id, promotion_code_id) REFERENCES promotion_codes (tenant_id, id)
);
-- One live redemption per target: asking twice for the same purchase is the same redemption.
CREATE UNIQUE INDEX coupon_redemptions_target ON coupon_redemptions (tenant_id, target_kind, target_id) WHERE reversed_at IS NULL;
CREATE INDEX coupon_redemptions_by_coupon ON coupon_redemptions (tenant_id, coupon_id, created_at DESC, id);
CREATE INDEX coupon_redemptions_by_code ON coupon_redemptions (tenant_id, promotion_code_id, customer_id) WHERE promotion_code_id IS NOT NULL;
CREATE INDEX coupon_redemptions_by_customer ON coupon_redemptions (tenant_id, customer_id) WHERE customer_id IS NOT NULL;

CREATE TABLE customer_coupons (
  tenant_id            uuid NOT NULL,
  id                   uuid NOT NULL DEFAULT gen_random_uuid(),
  customer_id          uuid NOT NULL,
  coupon_id            uuid NOT NULL,
  source               text NOT NULL CHECK (source <> ''),     -- who gave it: 'affiliate', 'operator'...
  source_ref           text NOT NULL,                          -- the programme's reference; one grant per (customer, source, ref)
  created_at           timestamptz NOT NULL DEFAULT now(),
  consumed_at          timestamptz,
  consumed_redemption_id uuid,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
  FOREIGN KEY (tenant_id, coupon_id) REFERENCES coupons (tenant_id, id),
  UNIQUE (tenant_id, customer_id, source, source_ref)
);
CREATE INDEX customer_coupons_pending ON customer_coupons (tenant_id, customer_id, created_at, id) WHERE consumed_at IS NULL;

ALTER TABLE subscription_coupons ADD COLUMN redemption_id uuid;

-- The migrator owns these tables and RLS is forced on them, so its statements see no rows (there is no tenant). The backfill below needs every tenant's rows:
-- the owner is exempt while FORCE is off, and it is switched on again before the migration ends.
ALTER TABLE coupons NO FORCE ROW LEVEL SECURITY;
ALTER TABLE subscriptions NO FORCE ROW LEVEL SECURITY;
ALTER TABLE subscription_coupons NO FORCE ROW LEVEL SECURITY;

-- Existing coupons: one promotion code each, with the same text. A code that differs from another only by case goes to the older coupon; the other stays
-- usable by an operator through the coupon itself.
INSERT INTO promotion_codes (tenant_id, coupon_id, code, active, redeemed, created_at)
SELECT tenant_id, id, code, active, redeemed, created_at FROM coupons ORDER BY created_at, code
ON CONFLICT DO NOTHING;

-- Their redemptions: a subscription that has the coupon is one; what the counter says beyond that (a coupon taken off again does not give its redemption back)
-- is legacy, so that the counter and the ledger agree from the start.
INSERT INTO coupon_redemptions (tenant_id, coupon_id, promotion_code_id, customer_id, target_kind, target_id, currency, created_at)
SELECT sc.tenant_id, c.id, p.id, s.customer_id, 'subscription', sc.subscription_id, s.currency, sc.redeemed_at
FROM subscription_coupons sc
JOIN coupons c ON c.tenant_id = sc.tenant_id AND c.code = sc.coupon_code
JOIN subscriptions s ON s.tenant_id = sc.tenant_id AND s.id = sc.subscription_id
LEFT JOIN promotion_codes p ON p.tenant_id = c.tenant_id AND p.coupon_id = c.id;

UPDATE subscription_coupons sc SET redemption_id = r.id
FROM coupon_redemptions r
WHERE r.tenant_id = sc.tenant_id AND r.target_kind = 'subscription' AND r.target_id = sc.subscription_id AND r.reversed_at IS NULL;

INSERT INTO coupon_redemptions (tenant_id, coupon_id, promotion_code_id, target_kind, target_id, created_at)
SELECT c.tenant_id, c.id, p.id, 'legacy', gen_random_uuid(), c.created_at
FROM coupons c
LEFT JOIN promotion_codes p ON p.tenant_id = c.tenant_id AND p.coupon_id = c.id
CROSS JOIN LATERAL generate_series(1, c.redeemed - (SELECT count(*) FROM coupon_redemptions r WHERE r.tenant_id = c.tenant_id AND r.coupon_id = c.id)::int);

ALTER TABLE coupons FORCE ROW LEVEL SECURITY;
ALTER TABLE subscriptions FORCE ROW LEVEL SECURITY;
ALTER TABLE subscription_coupons FORCE ROW LEVEL SECURITY;

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['promotion_codes', 'coupon_redemptions', 'customer_coupons'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE ON promotion_codes, coupon_redemptions, customer_coupons TO plans_app;
GRANT SELECT ON promotion_codes, coupon_redemptions, customer_coupons TO plans_system;

-- +goose Down
ALTER TABLE subscription_coupons DROP COLUMN redemption_id;
DROP TABLE customer_coupons, coupon_redemptions, promotion_codes;
-- A coupon of a kind the old schema cannot hold goes; the counter of the others is what the ledger said.
ALTER TABLE coupons NO FORCE ROW LEVEL SECURITY;   -- the owner sees every tenant's rows while FORCE is off
DELETE FROM coupons WHERE bonus_percent_bps IS NOT NULL;
ALTER TABLE coupons FORCE ROW LEVEL SECURITY;
ALTER TABLE coupons DROP CONSTRAINT bonus_coupon_is_for_credits, DROP CONSTRAINT coupon_is_percent_amount_or_bonus;
ALTER TABLE coupons ADD CONSTRAINT coupon_is_percent_or_amount CHECK ((percent_bps IS NOT NULL) <> (amount_minor IS NOT NULL));
ALTER TABLE coupons DROP CONSTRAINT coupons_id_key;
ALTER TABLE coupons DROP COLUMN applies_to, DROP COLUMN bonus_percent_bps, DROP COLUMN id;
