-- Phase 47, part 1: the affiliate program (partners, codes, clicks, first-touch attribution).
--
-- Everything is tenant data, so a sandbox and a live environment have separate programs. A tenant has at most one program (one row); its affiliates are partners
-- who bring customers with a link or a code. A click is counted, never recorded: one row per affiliate per day, no visitor, no address. An attribution says which
-- affiliate brought a customer; it is written once and never changes (first touch wins), and the database refuses an UPDATE or DELETE of it.
--
-- The code of an affiliate is unique across the platform, not just within its tenant: the public link /r/<code> carries no tenant, and the code is what finds it.
-- Commissions, payouts and the journal follow in 00048.
-- +goose Up
CREATE TABLE affiliate_programs (
  tenant_id               uuid PRIMARY KEY,
  status                  text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'paused')),
  commission_kind         text NOT NULL CHECK (commission_kind IN ('percent_bps', 'fixed')),
  -- Basis points of the net, pre-tax amount paid (1000 = 10 %), or an amount in minor units of `currency`.
  commission_value        bigint NOT NULL CHECK (commission_value > 0),
  currency                char(3) CHECK (currency ~ '^[A-Z]{3}$'),
  commission_term         text NOT NULL DEFAULT 'lifetime' CHECK (commission_term IN ('first_invoices', 'months', 'lifetime')),
  commission_term_value   int CHECK (commission_term_value BETWEEN 1 AND 120),
  attribution_window_days int NOT NULL DEFAULT 30 CHECK (attribution_window_days BETWEEN 1 AND 365),
  hold_days               int NOT NULL DEFAULT 30 CHECK (hold_days BETWEEN 0 AND 180),
  min_payout_minor        bigint NOT NULL DEFAULT 0 CHECK (min_payout_minor >= 0),
  referred_coupon_id      uuid,
  daily_click_cap         int NOT NULL DEFAULT 1000 CHECK (daily_click_cap BETWEEN 1 AND 100000),
  landing_url             text CHECK (landing_url IS NULL OR (landing_url ~ '^https?://' AND length(landing_url) <= 500)),
  auto_approve            boolean NOT NULL DEFAULT false,
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (tenant_id, referred_coupon_id) REFERENCES coupons (tenant_id, id),
  CONSTRAINT percent_is_at_most_all CHECK (commission_kind <> 'percent_bps' OR commission_value <= 10000),
  CONSTRAINT fixed_needs_currency CHECK (commission_kind <> 'fixed' OR currency IS NOT NULL),
  CONSTRAINT term_value_matches CHECK ((commission_term = 'lifetime') = (commission_term_value IS NULL))
);

CREATE TABLE affiliates (
  tenant_id       uuid NOT NULL,
  id              uuid NOT NULL DEFAULT gen_random_uuid(),
  code            text NOT NULL CHECK (code ~ '^[A-Za-z0-9_-]{3,32}$'),
  email           text NOT NULL CHECK (length(email) BETWEEN 3 AND 254),
  name            text NOT NULL DEFAULT '' CHECK (length(name) <= 200),
  -- The person who signs in as this affiliate (identity); NULL until they claim it with a verified e-mail address.
  user_id         uuid REFERENCES users (id),
  status          text NOT NULL CHECK (status IN ('pending', 'active', 'suspended')),
  payout_method   text NOT NULL DEFAULT 'manual' CHECK (payout_method IN ('manual', 'bank_transfer')),
  payout_details  text NOT NULL DEFAULT '' CHECK (length(payout_details) <= 1000),
  source          text NOT NULL DEFAULT 'owner' CHECK (source IN ('owner', 'application')),
  approved_at     timestamptz,
  suspended_at    timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id)
);
CREATE UNIQUE INDEX affiliates_code_key ON affiliates (upper(code));
CREATE UNIQUE INDEX affiliates_email_key ON affiliates (tenant_id, lower(email));
CREATE INDEX affiliates_by_user ON affiliates (user_id) WHERE user_id IS NOT NULL;
CREATE INDEX affiliates_by_email ON affiliates (lower(email)) WHERE user_id IS NULL;
CREATE INDEX affiliates_by_created ON affiliates (tenant_id, created_at DESC, id);

-- A promotion code that belongs to an affiliate: a customer who redeems it is attributed to that affiliate.
ALTER TABLE promotion_codes ADD COLUMN affiliate_id uuid;
ALTER TABLE promotion_codes ADD CONSTRAINT promotion_codes_affiliate_fkey FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates (tenant_id, id);
CREATE INDEX promotion_codes_by_affiliate ON promotion_codes (tenant_id, affiliate_id) WHERE affiliate_id IS NOT NULL;

CREATE TABLE affiliate_clicks_daily (
  tenant_id    uuid NOT NULL,
  affiliate_id uuid NOT NULL,
  day          date NOT NULL,
  clicks       int NOT NULL DEFAULT 0 CHECK (clicks >= 0),
  PRIMARY KEY (tenant_id, affiliate_id, day),
  FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates (tenant_id, id)
);

CREATE TABLE affiliate_attributions (
  tenant_id     uuid NOT NULL,
  customer_id   uuid NOT NULL,
  affiliate_id  uuid NOT NULL,
  source        text NOT NULL CHECK (source IN ('link', 'code', 'api')),
  -- When the visitor followed the link (or typed the code); attributed_at is when Payrudder wrote this row.
  touched_at    timestamptz NOT NULL,
  attributed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, customer_id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
  FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates (tenant_id, id)
);
CREATE INDEX affiliate_attributions_by_affiliate ON affiliate_attributions (tenant_id, affiliate_id, attributed_at DESC);

-- First touch wins: nothing moves an attribution afterwards, whatever the code above does.
-- +goose StatementBegin
CREATE FUNCTION affiliate_attributions_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'an affiliate attribution is set once and never changes' USING ERRCODE = 'P0001', HINT = 'attribution_immutable';
END $$;
-- +goose StatementEnd
CREATE TRIGGER affiliate_attributions_set_once BEFORE UPDATE OR DELETE ON affiliate_attributions
  FOR EACH ROW EXECUTE FUNCTION affiliate_attributions_immutable();

-- A customer who would have been attributed but looked like the affiliate themself. Kept so the owner can see why there is no commission.
CREATE TABLE affiliate_attribution_rejections (
  tenant_id    uuid NOT NULL,
  id           uuid NOT NULL DEFAULT gen_random_uuid(),
  affiliate_id uuid NOT NULL,
  customer_id  uuid NOT NULL,
  reason       text NOT NULL CHECK (reason IN ('same_email', 'same_domain', 'same_payment_method')),
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, affiliate_id, customer_id, reason),
  FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id)
);

-- The new role of a member of the owner's account: a partner who sees only their own data. Every account-wide read in identity leaves this role out.
ALTER TABLE account_members DROP CONSTRAINT account_members_role_check;
ALTER TABLE account_members ADD CONSTRAINT account_members_role_check CHECK (role IN ('owner', 'admin', 'viewer', 'affiliate'));

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['affiliate_programs', 'affiliates', 'affiliate_clicks_daily', 'affiliate_attributions', 'affiliate_attribution_rejections'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE ON affiliate_programs, affiliates, affiliate_clicks_daily TO plans_app;
GRANT SELECT, INSERT ON affiliate_attributions, affiliate_attribution_rejections TO plans_app;
-- The system role serves the public link (it has no tenant), the partner's own portal (a person, not a tenant) and the jobs.
GRANT SELECT ON affiliate_programs, affiliate_attributions, affiliate_attribution_rejections TO plans_system;
GRANT SELECT, UPDATE ON affiliates TO plans_system;
GRANT SELECT, INSERT, UPDATE ON affiliate_clicks_daily TO plans_system;
GRANT INSERT ON affiliates TO plans_system;

-- +goose Down
DELETE FROM account_members WHERE role = 'affiliate';
ALTER TABLE account_members DROP CONSTRAINT account_members_role_check;
ALTER TABLE account_members ADD CONSTRAINT account_members_role_check CHECK (role IN ('owner', 'admin', 'viewer'));
DROP TABLE affiliate_attribution_rejections;
DROP TRIGGER affiliate_attributions_set_once ON affiliate_attributions;
DROP TABLE affiliate_attributions;
DROP FUNCTION affiliate_attributions_immutable();
DROP TABLE affiliate_clicks_daily;
DROP INDEX promotion_codes_by_affiliate;
ALTER TABLE promotion_codes DROP CONSTRAINT promotion_codes_affiliate_fkey, DROP COLUMN affiliate_id;
DROP TABLE affiliates;
DROP TABLE affiliate_programs;
