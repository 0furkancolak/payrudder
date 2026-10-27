-- plans_db, tenant schema (phase 28, ADR 0018): what one tenant of a cell owns, in its own schema t_<id>.
--
-- It is the final shape of ../*.sql for the tables that have a tenant_id, written for a schema instead of public: the same tables, columns, constraints, indexes,
-- policies, triggers and privileges, nothing in it names a schema, and the functions bind the schema they are created in (SET search_path FROM CURRENT). It runs with
-- the search path set to the tenant's schema; each schema keeps its own goose_db_version. A change to ../ that touches one of these tables is a change here too:
-- TestTenantSchemaMatchesPublic compares the two. Roles are the service's own (plans_app, plans_system); the cell database has the same ones.
--
-- What is NOT here, and stays in the pooled database (the `shared` side of plans-svc: it owns the tenant directory itself): accounts, tenants, apps, users,
-- account_members, account_invites, user_sessions, user_tokens, user_identities, login_codes, user_onboarding, config_promotions, app_audit, go_live_audit, and two
-- tables that have a tenant_id but are looked up without knowing the tenant: api_keys (a key is found by its prefix, before there is a tenant: the gateway) and
-- sandbox_resets (the queue of the reset runner, which claims across tenants).
--
-- Differences from the public set, each forced by a table that is not here:
--   * affiliates.user_id has no foreign key to users(id). A partner is a person; the person lives in the pooled database, the partner record in the tenant's schema.
--   * current_tenant() cannot read tenants. It reads the caller's tenant row from the transaction setting app.tenant_row (a jsonb copy of the row, set by plans-svc
--     when it opens a tenant transaction in a cell) and returns it as the composite type `tenants`, declared here with the columns of the table. Same signature,
--     same use (CurrentTenant in the queries), another source.
--   * the two triggers that let a sandbox reset delete from an append-only table (affiliate_attributions, affiliate_journal) ask the same setting for the
--     environment instead of the tenants table.
--   * the btree_gist extension (the exclusion constraint of subscriptions) is created in `shared`, once per cell database, not in every tenant schema.

-- +goose Up
CREATE EXTENSION IF NOT EXISTS btree_gist WITH SCHEMA shared;

-- Tenant of the current transaction, set by pgxx.InTenantTx.
CREATE FUNCTION app_tenant_id() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE
AS $$ SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid $$;

-- The row of public.tenants in the pooled database, as a type, so that current_tenant() has the signature it has there.
CREATE TYPE tenants AS (
    id uuid,
    account_id uuid,
    environment text,
    name text,
    tier text,
    currency character(3),
    timezone text,
    grace_period interval,
    created_at timestamp with time zone,
    invoice_logo_asset_id uuid,
    app_id uuid,
    address jsonb,
    bank_transfer jsonb,
    legal jsonb,
    e_invoice_on text,
    status text,
    activated_at timestamp with time zone,
    terms_accepted_at timestamp with time zone,
    terms_accepted_by uuid,
    cell text,
    region text,
    placement_status text
);

-- The tenant's own row, from the setting plans-svc fills when it opens a transaction for a tenant in a cell. No setting, or the row of another tenant: no row.
-- +goose StatementBegin
CREATE FUNCTION current_tenant() RETURNS SETOF tenants
LANGUAGE sql STABLE SECURITY DEFINER SET search_path FROM CURRENT
AS $$
  SELECT r.* FROM jsonb_populate_record(NULL::tenants, NULLIF(current_setting('app.tenant_row', true), '')::jsonb) r
  WHERE r.id IS NOT DISTINCT FROM app_tenant_id() AND r.id IS NOT NULL
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION current_tenant() FROM PUBLIC;

-- Triggers. The two "set once" tables may be emptied by the reset of a sandbox.
-- +goose StatementBegin
CREATE FUNCTION affiliate_attributions_immutable() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT
AS $$
DECLARE
  tr jsonb := NULLIF(current_setting('app.tenant_row', true), '')::jsonb;
BEGIN
  IF TG_OP = 'DELETE' AND tr ->> 'environment' = 'sandbox' AND (tr ->> 'id')::uuid = OLD.tenant_id THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'an affiliate attribution is set once and never changes' USING ERRCODE = 'P0001', HINT = 'attribution_immutable';
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION affiliate_journal_append_only() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT
AS $$
DECLARE
  tr jsonb := NULLIF(current_setting('app.tenant_row', true), '')::jsonb;
BEGIN
  IF TG_OP = 'DELETE' AND tr ->> 'environment' = 'sandbox' AND (tr ->> 'id')::uuid = OLD.tenant_id THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'the affiliate journal is append-only' USING ERRCODE = 'P0001', HINT = 'journal_append_only';
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION affiliate_journal_balanced() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT
AS $$
DECLARE
  diff bigint;
BEGIN
  SELECT COALESCE(sum(debit_minor), 0) - COALESCE(sum(credit_minor), 0) INTO diff
  FROM affiliate_journal WHERE tenant_id = NEW.tenant_id AND payout_id = NEW.payout_id AND currency = NEW.currency;
  IF diff <> 0 THEN
    RAISE EXCEPTION 'the postings of a payout must balance: debits and credits differ by %', diff USING ERRCODE = 'P0001', HINT = 'journal_unbalanced';
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION webhook_endpoints_check_tenant() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT
AS $$
DECLARE
  t tenants;
BEGIN
  IF NEW.active THEN
    SELECT * INTO t FROM current_tenant();
    IF t.environment = 'live' AND t.status <> 'active' THEN
      RAISE EXCEPTION 'a live tenant needs to be active to have webhook endpoints' USING ERRCODE = 'P0001', HINT = 'tenant_inactive';
    END IF;
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TABLE affiliate_attribution_rejections (
    tenant_id uuid NOT NULL,
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    affiliate_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    reason text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT affiliate_attribution_rejections_reason_check CHECK ((reason = ANY (ARRAY['same_email'::text, 'same_domain'::text, 'same_payment_method'::text])))
);

ALTER TABLE affiliate_attribution_rejections FORCE ROW LEVEL SECURITY;

CREATE TABLE affiliate_attributions (
    tenant_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    affiliate_id uuid NOT NULL,
    source text NOT NULL,
    touched_at timestamp with time zone NOT NULL,
    attributed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT affiliate_attributions_source_check CHECK ((source = ANY (ARRAY['link'::text, 'code'::text, 'api'::text])))
);

ALTER TABLE affiliate_attributions FORCE ROW LEVEL SECURITY;

CREATE TABLE affiliate_clicks_daily (
    tenant_id uuid NOT NULL,
    affiliate_id uuid NOT NULL,
    day date NOT NULL,
    clicks integer DEFAULT 0 NOT NULL,
    CONSTRAINT affiliate_clicks_daily_clicks_check CHECK ((clicks >= 0))
);

ALTER TABLE affiliate_clicks_daily FORCE ROW LEVEL SECURITY;

CREATE TABLE affiliate_commissions (
    tenant_id uuid NOT NULL,
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    affiliate_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    kind text DEFAULT 'commission'::text NOT NULL,
    invoice_id uuid NOT NULL,
    invoice_number text DEFAULT ''::text NOT NULL,
    currency character(3) NOT NULL,
    invoice_total_minor bigint NOT NULL,
    net_minor bigint NOT NULL,
    amount_minor bigint NOT NULL,
    reversed_minor bigint DEFAULT 0 NOT NULL,
    status text NOT NULL,
    parent_id uuid,
    reversal_key text,
    earned_at timestamp with time zone NOT NULL,
    approve_after timestamp with time zone NOT NULL,
    approved_at timestamp with time zone,
    paid_at timestamp with time zone,
    payout_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    settled_minor bigint,
    CONSTRAINT affiliate_commissions_amount_minor_check CHECK ((amount_minor <> 0)),
    CONSTRAINT affiliate_commissions_currency_check CHECK ((currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT affiliate_commissions_invoice_total_minor_check CHECK ((invoice_total_minor >= 0)),
    CONSTRAINT affiliate_commissions_kind_check CHECK ((kind = ANY (ARRAY['commission'::text, 'reversal'::text]))),
    CONSTRAINT affiliate_commissions_net_minor_check CHECK ((net_minor >= 0)),
    CONSTRAINT affiliate_commissions_reversed_minor_check CHECK ((reversed_minor >= 0)),
    CONSTRAINT affiliate_commissions_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'approved'::text, 'paid'::text, 'reversed'::text]))),
    CONSTRAINT commission_settled_when_paid CHECK (((status = 'paid'::text) = (settled_minor IS NOT NULL))),
    CONSTRAINT commission_shape CHECK ((((kind = 'commission'::text) AND (amount_minor > 0) AND (parent_id IS NULL) AND (reversal_key IS NULL) AND (reversed_minor <= amount_minor)) OR ((kind = 'reversal'::text) AND (amount_minor < 0) AND (parent_id IS NOT NULL) AND (reversal_key IS NOT NULL) AND (reversed_minor = 0) AND (status = ANY (ARRAY['approved'::text, 'paid'::text]))))),
    CONSTRAINT commission_status_matches CHECK ((((status = 'paid'::text) = (paid_at IS NOT NULL)) AND ((status <> 'approved'::text) OR (approved_at IS NOT NULL))))
);

ALTER TABLE affiliate_commissions FORCE ROW LEVEL SECURITY;

CREATE TABLE affiliate_journal (
    tenant_id uuid NOT NULL,
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    payout_id uuid NOT NULL,
    affiliate_id uuid NOT NULL,
    posted_at timestamp with time zone NOT NULL,
    currency character(3) NOT NULL,
    account text NOT NULL,
    debit_minor bigint DEFAULT 0 NOT NULL,
    credit_minor bigint DEFAULT 0 NOT NULL,
    memo text DEFAULT ''::text NOT NULL,
    sandbox boolean NOT NULL,
    CONSTRAINT affiliate_journal_account_check CHECK ((account = ANY (ARRAY['commission_expense'::text, 'commissions_payable'::text, 'cash'::text]))),
    CONSTRAINT affiliate_journal_credit_minor_check CHECK ((credit_minor >= 0)),
    CONSTRAINT affiliate_journal_currency_check CHECK ((currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT affiliate_journal_debit_minor_check CHECK ((debit_minor >= 0)),
    CONSTRAINT one_side CHECK (((debit_minor > 0) <> (credit_minor > 0)))
);

ALTER TABLE affiliate_journal FORCE ROW LEVEL SECURITY;

CREATE TABLE affiliate_payouts (
    tenant_id uuid NOT NULL,
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    affiliate_id uuid NOT NULL,
    currency character(3) NOT NULL,
    total_minor bigint NOT NULL,
    commission_count integer NOT NULL,
    method text NOT NULL,
    payout_details text DEFAULT ''::text NOT NULL,
    reference text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'open'::text NOT NULL,
    sandbox boolean NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    paid_at timestamp with time zone,
    cancelled_at timestamp with time zone,
    CONSTRAINT affiliate_payouts_commission_count_check CHECK ((commission_count >= 0)),
    CONSTRAINT affiliate_payouts_currency_check CHECK ((currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT affiliate_payouts_method_check CHECK ((method = ANY (ARRAY['manual'::text, 'bank_transfer'::text]))),
    CONSTRAINT affiliate_payouts_reference_check CHECK ((length(reference) <= 200)),
    CONSTRAINT affiliate_payouts_status_check CHECK ((status = ANY (ARRAY['open'::text, 'paid'::text, 'cancelled'::text]))),
    CONSTRAINT paid_payout_is_positive CHECK (((status <> 'paid'::text) OR (total_minor > 0))),
    CONSTRAINT payout_state CHECK ((((status = 'paid'::text) = (paid_at IS NOT NULL)) AND ((status = 'cancelled'::text) = (cancelled_at IS NOT NULL))))
);

ALTER TABLE affiliate_payouts FORCE ROW LEVEL SECURITY;

CREATE TABLE affiliate_programs (
    tenant_id uuid NOT NULL,
    status text DEFAULT 'draft'::text NOT NULL,
    commission_kind text NOT NULL,
    commission_value bigint NOT NULL,
    currency character(3),
    commission_term text DEFAULT 'lifetime'::text NOT NULL,
    commission_term_value integer,
    attribution_window_days integer DEFAULT 30 NOT NULL,
    hold_days integer DEFAULT 30 NOT NULL,
    min_payout_minor bigint DEFAULT 0 NOT NULL,
    referred_coupon_id uuid,
    daily_click_cap integer DEFAULT 1000 NOT NULL,
    landing_url text,
    auto_approve boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT affiliate_programs_attribution_window_days_check CHECK (((attribution_window_days >= 1) AND (attribution_window_days <= 365))),
    CONSTRAINT affiliate_programs_commission_kind_check CHECK ((commission_kind = ANY (ARRAY['percent_bps'::text, 'fixed'::text]))),
    CONSTRAINT affiliate_programs_commission_term_check CHECK ((commission_term = ANY (ARRAY['first_invoices'::text, 'months'::text, 'lifetime'::text]))),
    CONSTRAINT affiliate_programs_commission_term_value_check CHECK (((commission_term_value >= 1) AND (commission_term_value <= 120))),
    CONSTRAINT affiliate_programs_commission_value_check CHECK ((commission_value > 0)),
    CONSTRAINT affiliate_programs_currency_check CHECK ((currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT affiliate_programs_daily_click_cap_check CHECK (((daily_click_cap >= 1) AND (daily_click_cap <= 100000))),
    CONSTRAINT affiliate_programs_hold_days_check CHECK (((hold_days >= 0) AND (hold_days <= 180))),
    CONSTRAINT affiliate_programs_landing_url_check CHECK (((landing_url IS NULL) OR ((landing_url ~ '^https?://'::text) AND (length(landing_url) <= 500)))),
    CONSTRAINT affiliate_programs_min_payout_minor_check CHECK ((min_payout_minor >= 0)),
    CONSTRAINT affiliate_programs_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'active'::text, 'paused'::text]))),
    CONSTRAINT fixed_needs_currency CHECK (((commission_kind <> 'fixed'::text) OR (currency IS NOT NULL))),
    CONSTRAINT percent_is_at_most_all CHECK (((commission_kind <> 'percent_bps'::text) OR (commission_value <= 10000))),
    CONSTRAINT term_value_matches CHECK (((commission_term = 'lifetime'::text) = (commission_term_value IS NULL)))
);

ALTER TABLE affiliate_programs FORCE ROW LEVEL SECURITY;

CREATE TABLE affiliate_reversals (
    tenant_id uuid NOT NULL,
    key text NOT NULL,
    commission_id uuid NOT NULL,
    invoice_id uuid NOT NULL,
    kind text NOT NULL,
    credited_minor bigint DEFAULT 0 NOT NULL,
    amount_minor bigint NOT NULL,
    note text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT affiliate_reversals_amount_minor_check CHECK ((amount_minor >= 0)),
    CONSTRAINT affiliate_reversals_credited_minor_check CHECK ((credited_minor >= 0)),
    CONSTRAINT affiliate_reversals_key_check CHECK (((length(key) >= 1) AND (length(key) <= 300))),
    CONSTRAINT affiliate_reversals_kind_check CHECK ((kind = ANY (ARRAY['void'::text, 'credit_note'::text, 'chargeback'::text, 'manual'::text]))),
    CONSTRAINT affiliate_reversals_note_check CHECK ((length(note) <= 500))
);

ALTER TABLE affiliate_reversals FORCE ROW LEVEL SECURITY;

CREATE TABLE affiliates (
    tenant_id uuid NOT NULL,
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    code text NOT NULL,
    email text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    user_id uuid,
    status text NOT NULL,
    payout_method text DEFAULT 'manual'::text NOT NULL,
    payout_details text DEFAULT ''::text NOT NULL,
    source text DEFAULT 'owner'::text NOT NULL,
    approved_at timestamp with time zone,
    suspended_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT affiliates_code_check CHECK ((code ~ '^[A-Za-z0-9_-]{3,32}$'::text)),
    CONSTRAINT affiliates_email_check CHECK (((length(email) >= 3) AND (length(email) <= 254))),
    CONSTRAINT affiliates_name_check CHECK ((length(name) <= 200)),
    CONSTRAINT affiliates_payout_details_check CHECK ((length(payout_details) <= 1000)),
    CONSTRAINT affiliates_payout_method_check CHECK ((payout_method = ANY (ARRAY['manual'::text, 'bank_transfer'::text]))),
    CONSTRAINT affiliates_source_check CHECK ((source = ANY (ARRAY['owner'::text, 'application'::text]))),
    CONSTRAINT affiliates_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'active'::text, 'suspended'::text])))
);

ALTER TABLE affiliates FORCE ROW LEVEL SECURITY;

CREATE TABLE coupon_redemptions (
    tenant_id uuid NOT NULL,
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    coupon_id uuid NOT NULL,
    promotion_code_id uuid,
    customer_id uuid,
    target_kind text NOT NULL,
    target_id uuid NOT NULL,
    invoice_id uuid,
    discount_minor bigint DEFAULT 0 NOT NULL,
    bonus_credits bigint DEFAULT 0 NOT NULL,
    currency character(3),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    reversed_at timestamp with time zone,
    slot_returned boolean DEFAULT false NOT NULL,
    CONSTRAINT coupon_redemptions_bonus_credits_check CHECK ((bonus_credits >= 0)),
    CONSTRAINT coupon_redemptions_discount_minor_check CHECK ((discount_minor >= 0)),
    CONSTRAINT coupon_redemptions_target_kind_check CHECK ((target_kind = ANY (ARRAY['subscription'::text, 'credit_purchase'::text, 'payment'::text, 'legacy'::text])))
);

ALTER TABLE coupon_redemptions FORCE ROW LEVEL SECURITY;

CREATE TABLE coupons (
    tenant_id uuid NOT NULL,
    code text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    percent_bps integer,
    amount_minor bigint,
    currency character(3),
    duration text NOT NULL,
    duration_periods integer,
    max_redemptions integer,
    redeemed integer DEFAULT 0 NOT NULL,
    expires_at timestamp with time zone,
    active boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    bonus_percent_bps integer,
    applies_to text[] DEFAULT ARRAY['subscription'::text, 'credit_purchase'::text, 'payment'::text] NOT NULL,
    CONSTRAINT amount_coupon_has_currency CHECK (((amount_minor IS NULL) OR (currency IS NOT NULL))),
    CONSTRAINT bonus_coupon_is_for_credits CHECK (((bonus_percent_bps IS NULL) OR (applies_to = ARRAY['credit_purchase'::text]))),
    CONSTRAINT coupon_is_percent_amount_or_bonus CHECK ((num_nonnulls(percent_bps, amount_minor, bonus_percent_bps) = 1)),
    CONSTRAINT coupons_amount_minor_check CHECK ((amount_minor > 0)),
    CONSTRAINT coupons_applies_to_check CHECK (((cardinality(applies_to) >= 1) AND (applies_to <@ ARRAY['subscription'::text, 'credit_purchase'::text, 'payment'::text]))),
    CONSTRAINT coupons_bonus_percent_bps_check CHECK (((bonus_percent_bps >= 1) AND (bonus_percent_bps <= 100000))),
    CONSTRAINT coupons_code_check CHECK ((code ~ '^[A-Za-z0-9_-]{2,40}$'::text)),
    CONSTRAINT coupons_currency_check CHECK ((currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT coupons_duration_check CHECK ((duration = ANY (ARRAY['once'::text, 'repeating'::text, 'forever'::text]))),
    CONSTRAINT coupons_duration_periods_check CHECK ((duration_periods >= 1)),
    CONSTRAINT coupons_max_redemptions_check CHECK ((max_redemptions >= 1)),
    CONSTRAINT coupons_percent_bps_check CHECK (((percent_bps >= 1) AND (percent_bps <= 10000))),
    CONSTRAINT coupons_redeemed_check CHECK ((redeemed >= 0)),
    CONSTRAINT repeating_has_periods CHECK (((duration = 'repeating'::text) = (duration_periods IS NOT NULL)))
);

ALTER TABLE coupons FORCE ROW LEVEL SECURITY;

CREATE TABLE customer_coupons (
    tenant_id uuid NOT NULL,
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    customer_id uuid NOT NULL,
    coupon_id uuid NOT NULL,
    source text NOT NULL,
    source_ref text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    consumed_at timestamp with time zone,
    consumed_redemption_id uuid,
    CONSTRAINT customer_coupons_source_check CHECK ((source <> ''::text))
);

ALTER TABLE customer_coupons FORCE ROW LEVEL SECURITY;

CREATE TABLE customer_entitlement_overrides (
    tenant_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    meter_code text NOT NULL,
    limit_units bigint NOT NULL,
    alert_thresholds_pct integer[] DEFAULT '{80,100}'::integer[] NOT NULL,
    quota_window text DEFAULT 'month'::text NOT NULL,
    mode text DEFAULT 'hard'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT customer_entitlement_overrides_limit_units_check CHECK ((limit_units >= 0)),
    CONSTRAINT customer_entitlement_overrides_mode_check CHECK ((mode = ANY (ARRAY['hard'::text, 'soft'::text, 'notify'::text]))),
    CONSTRAINT customer_entitlement_overrides_quota_window_check CHECK ((quota_window = ANY (ARRAY['hour'::text, 'day'::text, 'week'::text, 'month'::text, 'year'::text, 'lifetime'::text])))
);

ALTER TABLE customer_entitlement_overrides FORCE ROW LEVEL SECURITY;

CREATE TABLE customers (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    external_id text NOT NULL,
    name text NOT NULL,
    email text NOT NULL,
    payment_method text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    country character(2),
    billing_currency character(3),
    address jsonb DEFAULT '{}'::jsonb NOT NULL,
    tax_id text DEFAULT ''::text NOT NULL,
    tax_office text DEFAULT ''::text NOT NULL,
    e_invoice_registered boolean DEFAULT false NOT NULL,
    CONSTRAINT customers_address_check CHECK ((jsonb_typeof(address) = 'object'::text)),
    CONSTRAINT customers_billing_currency_check CHECK ((billing_currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT customers_country_check CHECK ((country ~ '^[A-Z]{2}$'::text)),
    CONSTRAINT customers_external_id_check CHECK (((length(external_id) >= 1) AND (length(external_id) <= 128)))
);

ALTER TABLE customers FORCE ROW LEVEL SECURITY;

CREATE TABLE meters (
    tenant_id uuid NOT NULL,
    code text NOT NULL,
    version integer NOT NULL,
    aggregation text NOT NULL,
    value_property text,
    filter jsonb DEFAULT '{}'::jsonb NOT NULL,
    quantity_scale smallint NOT NULL,
    unit_label text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    category text DEFAULT ''::text NOT NULL,
    CONSTRAINT meters_aggregation_check CHECK ((aggregation = ANY (ARRAY['SUM'::text, 'COUNT'::text, 'MAX'::text, 'UNIQUE'::text, 'LAST'::text]))),
    CONSTRAINT meters_category_check CHECK ((length(category) <= 64)),
    CONSTRAINT meters_check CHECK (((aggregation = 'COUNT'::text) = (value_property IS NULL))),
    CONSTRAINT meters_code_check CHECK ((code ~ '^[a-z][a-z0-9_]{0,62}$'::text)),
    CONSTRAINT meters_quantity_scale_check CHECK (((quantity_scale >= 0) AND (quantity_scale <= 6))),
    CONSTRAINT meters_version_check CHECK ((version > 0))
);

ALTER TABLE meters FORCE ROW LEVEL SECURITY;

CREATE TABLE outbox (
    id bigint NOT NULL,
    tenant_id uuid NOT NULL,
    topic text NOT NULL,
    partition_key text NOT NULL,
    payload bytea,
    headers jsonb DEFAULT '{}'::jsonb NOT NULL,
    ingested_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE outbox ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME outbox_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE plan_currency_prices (
    tenant_id uuid NOT NULL,
    plan_version_id uuid NOT NULL,
    currency character(3) NOT NULL,
    meter_code text NOT NULL,
    model text NOT NULL,
    config jsonb NOT NULL,
    CONSTRAINT plan_currency_prices_currency_check CHECK ((currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT plan_currency_prices_model_check CHECK ((model = ANY (ARRAY['PER_UNIT'::text, 'TIERED_GRADUATED'::text, 'PACKAGE'::text])))
);

ALTER TABLE plan_currency_prices FORCE ROW LEVEL SECURITY;

CREATE TABLE plan_entitlements (
    tenant_id uuid NOT NULL,
    plan_version_id uuid NOT NULL,
    meter_code text NOT NULL,
    limit_units bigint NOT NULL,
    alert_thresholds_pct integer[] DEFAULT '{80,100}'::integer[] NOT NULL,
    quota_window text DEFAULT 'month'::text NOT NULL,
    mode text DEFAULT 'hard'::text NOT NULL,
    CONSTRAINT plan_entitlements_limit_units_check CHECK ((limit_units >= 0)),
    CONSTRAINT plan_entitlements_mode_check CHECK ((mode = ANY (ARRAY['hard'::text, 'soft'::text, 'notify'::text]))),
    CONSTRAINT plan_entitlements_quota_window_check CHECK ((quota_window = ANY (ARRAY['hour'::text, 'day'::text, 'week'::text, 'month'::text, 'year'::text, 'lifetime'::text])))
);

ALTER TABLE plan_entitlements FORCE ROW LEVEL SECURITY;

CREATE TABLE plan_prices (
    tenant_id uuid NOT NULL,
    plan_version_id uuid NOT NULL,
    meter_code text NOT NULL,
    model text NOT NULL,
    config jsonb NOT NULL,
    CONSTRAINT plan_prices_model_check CHECK ((model = ANY (ARRAY['PER_UNIT'::text, 'TIERED_GRADUATED'::text, 'PACKAGE'::text])))
);

ALTER TABLE plan_prices FORCE ROW LEVEL SECURITY;

CREATE TABLE plan_version_fees (
    tenant_id uuid NOT NULL,
    plan_version_id uuid NOT NULL,
    currency character(3) NOT NULL,
    base_fee_minor bigint NOT NULL,
    "interval" text DEFAULT 'month'::text NOT NULL,
    CONSTRAINT plan_version_fees_base_fee_minor_check CHECK ((base_fee_minor >= 0)),
    CONSTRAINT plan_version_fees_currency_check CHECK ((currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT plan_version_fees_interval_check CHECK (("interval" = ANY (ARRAY['month'::text, 'year'::text])))
);

ALTER TABLE plan_version_fees FORCE ROW LEVEL SECURITY;

CREATE TABLE plan_versions (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    plan_id uuid NOT NULL,
    version integer NOT NULL,
    base_fee_minor bigint DEFAULT 0 NOT NULL,
    published_at timestamp with time zone DEFAULT now() NOT NULL,
    trial_days integer DEFAULT 0 NOT NULL,
    CONSTRAINT plan_versions_base_fee_minor_check CHECK ((base_fee_minor >= 0)),
    CONSTRAINT plan_versions_trial_days_check CHECK (((trial_days >= 0) AND (trial_days <= 365))),
    CONSTRAINT plan_versions_version_check CHECK ((version > 0))
);

ALTER TABLE plan_versions FORCE ROW LEVEL SECURITY;

CREATE TABLE plans (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    code text NOT NULL,
    name text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT plans_code_check CHECK ((code ~ '^[a-z][a-z0-9_-]{0,62}$'::text))
);

ALTER TABLE plans FORCE ROW LEVEL SECURITY;

CREATE TABLE promotion_codes (
    tenant_id uuid NOT NULL,
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    coupon_id uuid NOT NULL,
    code text NOT NULL,
    active boolean DEFAULT true NOT NULL,
    max_redemptions integer,
    redeemed integer DEFAULT 0 NOT NULL,
    expires_at timestamp with time zone,
    first_purchase_only boolean DEFAULT false NOT NULL,
    one_per_customer boolean DEFAULT false NOT NULL,
    min_amount_minor bigint,
    min_amount_currency character(3),
    allowed_plans text[] DEFAULT '{}'::text[] NOT NULL,
    allowed_packs text[] DEFAULT '{}'::text[] NOT NULL,
    allowed_currencies text[] DEFAULT '{}'::text[] NOT NULL,
    restore_on_reversal boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    affiliate_id uuid,
    CONSTRAINT min_amount_has_currency CHECK (((min_amount_minor IS NULL) = (min_amount_currency IS NULL))),
    CONSTRAINT promotion_codes_code_check CHECK ((code ~ '^[A-Za-z0-9_-]{2,40}$'::text)),
    CONSTRAINT promotion_codes_max_redemptions_check CHECK ((max_redemptions >= 1)),
    CONSTRAINT promotion_codes_min_amount_currency_check CHECK ((min_amount_currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT promotion_codes_min_amount_minor_check CHECK ((min_amount_minor > 0)),
    CONSTRAINT promotion_codes_redeemed_check CHECK ((redeemed >= 0))
);

ALTER TABLE promotion_codes FORCE ROW LEVEL SECURITY;

CREATE TABLE provider_migrations (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    subscription_id uuid NOT NULL,
    new_subscription_id uuid,
    from_provider text DEFAULT ''::text NOT NULL,
    to_provider text NOT NULL,
    state text NOT NULL,
    cutover_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_migrations_state_check CHECK ((state = ANY (ARRAY['started'::text, 'completed'::text, 'aborted'::text]))),
    CONSTRAINT provider_migrations_to_provider_check CHECK ((to_provider ~ '^[a-z][a-z0-9_-]{0,31}$'::text))
);

ALTER TABLE provider_migrations FORCE ROW LEVEL SECURITY;

CREATE TABLE routing_rules (
    tenant_id uuid NOT NULL,
    "position" integer NOT NULL,
    country character(2),
    currency character(3),
    provider text NOT NULL,
    CONSTRAINT routing_rules_country_check CHECK ((country ~ '^[A-Z]{2}$'::text)),
    CONSTRAINT routing_rules_currency_check CHECK ((currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT routing_rules_position_check CHECK (("position" >= 0)),
    CONSTRAINT routing_rules_provider_check CHECK ((provider ~ '^[a-z][a-z0-9_-]{0,31}$'::text))
);

ALTER TABLE routing_rules FORCE ROW LEVEL SECURITY;

CREATE TABLE subscription_adjustments (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    subscription_id uuid NOT NULL,
    old_plan_code text NOT NULL,
    from_at timestamp with time zone NOT NULL,
    to_at timestamp with time zone NOT NULL,
    credit_minor bigint NOT NULL,
    charge_minor bigint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    billed_at timestamp with time zone,
    CONSTRAINT subscription_adjustments_charge_minor_check CHECK ((charge_minor >= 0)),
    CONSTRAINT subscription_adjustments_credit_minor_check CHECK ((credit_minor >= 0))
);

ALTER TABLE subscription_adjustments FORCE ROW LEVEL SECURITY;

CREATE TABLE subscription_changes (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    subscription_id uuid NOT NULL,
    plan_version_id uuid NOT NULL,
    "interval" text NOT NULL,
    effective_at timestamp with time zone NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    applied_at timestamp with time zone,
    new_subscription_id uuid,
    quantity integer,
    CONSTRAINT subscription_changes_interval_check CHECK (("interval" = ANY (ARRAY['month'::text, 'year'::text]))),
    CONSTRAINT subscription_changes_quantity_check CHECK (((quantity >= 1) AND (quantity <= 100000))),
    CONSTRAINT subscription_changes_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'applied'::text, 'cancelled'::text])))
);

ALTER TABLE subscription_changes FORCE ROW LEVEL SECURITY;

CREATE TABLE subscription_coupons (
    tenant_id uuid NOT NULL,
    subscription_id uuid NOT NULL,
    coupon_code text NOT NULL,
    redeemed_at timestamp with time zone DEFAULT now() NOT NULL,
    periods_applied integer DEFAULT 0 NOT NULL,
    redemption_id uuid
);

ALTER TABLE subscription_coupons FORCE ROW LEVEL SECURITY;

CREATE TABLE subscription_trial_notices (
    tenant_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    trial_ends_at timestamp with time zone NOT NULL,
    subscription_id uuid NOT NULL,
    notified_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE subscription_trial_notices FORCE ROW LEVEL SECURITY;

CREATE TABLE subscriptions (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    customer_id uuid NOT NULL,
    plan_version_id uuid NOT NULL,
    active tstzrange NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    state text DEFAULT 'active'::text NOT NULL,
    "interval" text DEFAULT 'month'::text NOT NULL,
    currency character(3) NOT NULL,
    trial_ends_at timestamp with time zone,
    cancel_at_period_end boolean DEFAULT false NOT NULL,
    paused_at timestamp with time zone,
    billing_anchor timestamp with time zone NOT NULL,
    changed_from_id uuid,
    provider text,
    provider_ref text,
    owner text DEFAULT 'payrudder'::text NOT NULL,
    provider_synced_at timestamp with time zone,
    fee_timing text DEFAULT 'arrears'::text NOT NULL,
    fee_due_at timestamp with time zone,
    quantity integer DEFAULT 1 NOT NULL,
    CONSTRAINT provider_owned_has_provider CHECK (((owner = 'payrudder'::text) OR ((provider IS NOT NULL) AND (provider_ref IS NOT NULL)))),
    CONSTRAINT subscriptions_active_check CHECK (((NOT isempty(active)) AND (lower(active) IS NOT NULL))),
    CONSTRAINT subscriptions_fee_timing_check CHECK ((fee_timing = ANY (ARRAY['advance'::text, 'arrears'::text]))),
    CONSTRAINT subscriptions_interval_check CHECK (("interval" = ANY (ARRAY['month'::text, 'year'::text]))),
    CONSTRAINT subscriptions_owner_check CHECK ((owner = ANY (ARRAY['payrudder'::text, 'provider'::text]))),
    CONSTRAINT subscriptions_provider_check CHECK ((provider ~ '^[a-z][a-z0-9_-]{0,31}$'::text)),
    CONSTRAINT subscriptions_quantity_check CHECK (((quantity >= 1) AND (quantity <= 100000))),
    CONSTRAINT subscriptions_state_check CHECK ((state = ANY (ARRAY['trialing'::text, 'active'::text, 'past_due'::text, 'paused'::text, 'cancelled'::text])))
);

ALTER TABLE subscriptions FORCE ROW LEVEL SECURITY;

CREATE TABLE tax_rates (
    tenant_id uuid NOT NULL,
    country text NOT NULL,
    name text NOT NULL,
    rate_bps integer NOT NULL,
    CONSTRAINT tax_rates_country_check CHECK (((country = ''::text) OR (country ~ '^[A-Z]{2}$'::text))),
    CONSTRAINT tax_rates_name_check CHECK (((length(name) >= 1) AND (length(name) <= 40))),
    CONSTRAINT tax_rates_rate_bps_check CHECK (((rate_bps >= 0) AND (rate_bps <= 10000)))
);

ALTER TABLE tax_rates FORCE ROW LEVEL SECURITY;

CREATE TABLE tenant_mail_integrations (
    tenant_id uuid NOT NULL,
    provider text NOT NULL,
    base_url text NOT NULL,
    api_key_enc bytea NOT NULL,
    key_hint text DEFAULT ''::text NOT NULL,
    from_address text NOT NULL,
    checked_at timestamp with time zone,
    check_problem text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tenant_mail_integrations_base_url_check CHECK ((base_url <> ''::text)),
    CONSTRAINT tenant_mail_integrations_check_problem_check CHECK ((check_problem = ANY (ARRAY[''::text, 'unreachable'::text, 'refused'::text]))),
    CONSTRAINT tenant_mail_integrations_from_address_check CHECK ((from_address <> ''::text)),
    CONSTRAINT tenant_mail_integrations_provider_check CHECK ((provider = 'nafru'::text))
);

ALTER TABLE tenant_mail_integrations FORCE ROW LEVEL SECURITY;

CREATE TABLE tenant_mail_settings (
    tenant_id uuid NOT NULL,
    disabled text[] DEFAULT '{}'::text[] NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tenant_mail_settings_disabled_check CHECK ((disabled <@ ARRAY['invoice_issued'::text, 'payment_receipts'::text, 'payment_failed'::text, 'usage_alerts'::text, 'system'::text]))
);

ALTER TABLE tenant_mail_settings FORCE ROW LEVEL SECURITY;

CREATE TABLE webhook_deliveries (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    event_id uuid NOT NULL,
    endpoint_id uuid NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    last_status integer,
    last_error text,
    last_attempt_at timestamp with time zone,
    delivered_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT webhook_deliveries_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'delivered'::text, 'failed'::text])))
);

ALTER TABLE webhook_deliveries FORCE ROW LEVEL SECURITY;

CREATE TABLE webhook_endpoints (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    url text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    event_types text[] DEFAULT '{}'::text[] NOT NULL,
    secret_enc bytea NOT NULL,
    prev_secret_enc bytea,
    prev_secret_until timestamp with time zone,
    active boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT webhook_endpoints_description_check CHECK ((length(description) <= 200)),
    CONSTRAINT webhook_endpoints_url_check CHECK ((length(url) <= 2000))
);

ALTER TABLE webhook_endpoints FORCE ROW LEVEL SECURITY;

CREATE TABLE webhook_events (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    type text NOT NULL,
    data jsonb NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE webhook_events FORCE ROW LEVEL SECURITY;

ALTER TABLE ONLY affiliate_attribution_rejections
    ADD CONSTRAINT affiliate_attribution_rejecti_tenant_id_affiliate_id_custom_key UNIQUE (tenant_id, affiliate_id, customer_id, reason);

ALTER TABLE ONLY affiliate_attribution_rejections
    ADD CONSTRAINT affiliate_attribution_rejections_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY affiliate_attributions
    ADD CONSTRAINT affiliate_attributions_pkey PRIMARY KEY (tenant_id, customer_id);

ALTER TABLE ONLY affiliate_clicks_daily
    ADD CONSTRAINT affiliate_clicks_daily_pkey PRIMARY KEY (tenant_id, affiliate_id, day);

ALTER TABLE ONLY affiliate_commissions
    ADD CONSTRAINT affiliate_commissions_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY affiliate_journal
    ADD CONSTRAINT affiliate_journal_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY affiliate_payouts
    ADD CONSTRAINT affiliate_payouts_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY affiliate_programs
    ADD CONSTRAINT affiliate_programs_pkey PRIMARY KEY (tenant_id);

ALTER TABLE ONLY affiliate_reversals
    ADD CONSTRAINT affiliate_reversals_pkey PRIMARY KEY (tenant_id, key);

ALTER TABLE ONLY affiliates
    ADD CONSTRAINT affiliates_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY coupon_redemptions
    ADD CONSTRAINT coupon_redemptions_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY coupons
    ADD CONSTRAINT coupons_id_key UNIQUE (tenant_id, id);

ALTER TABLE ONLY coupons
    ADD CONSTRAINT coupons_pkey PRIMARY KEY (tenant_id, code);

ALTER TABLE ONLY customer_coupons
    ADD CONSTRAINT customer_coupons_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY customer_coupons
    ADD CONSTRAINT customer_coupons_tenant_id_customer_id_source_source_ref_key UNIQUE (tenant_id, customer_id, source, source_ref);

ALTER TABLE ONLY customer_entitlement_overrides
    ADD CONSTRAINT customer_entitlement_overrides_pkey PRIMARY KEY (tenant_id, customer_id, meter_code);

ALTER TABLE ONLY customers
    ADD CONSTRAINT customers_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY customers
    ADD CONSTRAINT customers_tenant_id_external_id_key UNIQUE (tenant_id, external_id);

ALTER TABLE ONLY meters
    ADD CONSTRAINT meters_pkey PRIMARY KEY (tenant_id, code, version);

ALTER TABLE ONLY subscriptions
    ADD CONSTRAINT one_active_subscription EXCLUDE USING gist (tenant_id WITH =, customer_id WITH =, active WITH &&);

ALTER TABLE ONLY outbox
    ADD CONSTRAINT outbox_pkey PRIMARY KEY (id);

ALTER TABLE ONLY plan_currency_prices
    ADD CONSTRAINT plan_currency_prices_pkey PRIMARY KEY (tenant_id, plan_version_id, currency, meter_code);

ALTER TABLE ONLY plan_entitlements
    ADD CONSTRAINT plan_entitlements_pkey PRIMARY KEY (tenant_id, plan_version_id, meter_code);

ALTER TABLE ONLY plan_prices
    ADD CONSTRAINT plan_prices_pkey PRIMARY KEY (tenant_id, plan_version_id, meter_code);

ALTER TABLE ONLY plan_version_fees
    ADD CONSTRAINT plan_version_fees_pkey PRIMARY KEY (tenant_id, plan_version_id, currency, "interval");

ALTER TABLE ONLY plan_versions
    ADD CONSTRAINT plan_versions_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY plan_versions
    ADD CONSTRAINT plan_versions_tenant_id_plan_id_version_key UNIQUE (tenant_id, plan_id, version);

ALTER TABLE ONLY plans
    ADD CONSTRAINT plans_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY plans
    ADD CONSTRAINT plans_tenant_id_code_key UNIQUE (tenant_id, code);

ALTER TABLE ONLY promotion_codes
    ADD CONSTRAINT promotion_codes_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY provider_migrations
    ADD CONSTRAINT provider_migrations_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY routing_rules
    ADD CONSTRAINT routing_rules_pkey PRIMARY KEY (tenant_id, "position");

ALTER TABLE ONLY subscription_adjustments
    ADD CONSTRAINT subscription_adjustments_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY subscription_changes
    ADD CONSTRAINT subscription_changes_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY subscription_coupons
    ADD CONSTRAINT subscription_coupons_pkey PRIMARY KEY (tenant_id, subscription_id);

ALTER TABLE ONLY subscription_trial_notices
    ADD CONSTRAINT subscription_trial_notices_pkey PRIMARY KEY (tenant_id, customer_id, trial_ends_at);

ALTER TABLE ONLY subscriptions
    ADD CONSTRAINT subscriptions_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY tax_rates
    ADD CONSTRAINT tax_rates_pkey PRIMARY KEY (tenant_id, country);

ALTER TABLE ONLY tenant_mail_integrations
    ADD CONSTRAINT tenant_mail_integrations_pkey PRIMARY KEY (tenant_id);

ALTER TABLE ONLY tenant_mail_settings
    ADD CONSTRAINT tenant_mail_settings_pkey PRIMARY KEY (tenant_id);

ALTER TABLE ONLY webhook_deliveries
    ADD CONSTRAINT webhook_deliveries_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY webhook_deliveries
    ADD CONSTRAINT webhook_deliveries_tenant_id_event_id_endpoint_id_key UNIQUE (tenant_id, event_id, endpoint_id);

ALTER TABLE ONLY webhook_endpoints
    ADD CONSTRAINT webhook_endpoints_pkey PRIMARY KEY (tenant_id, id);

ALTER TABLE ONLY webhook_events
    ADD CONSTRAINT webhook_events_pkey PRIMARY KEY (tenant_id, id);

CREATE INDEX affiliate_attributions_by_affiliate ON affiliate_attributions USING btree (tenant_id, affiliate_id, attributed_at DESC);

CREATE INDEX affiliate_commissions_by_affiliate ON affiliate_commissions USING btree (tenant_id, affiliate_id, status, earned_at DESC, id);

CREATE INDEX affiliate_commissions_by_customer ON affiliate_commissions USING btree (tenant_id, customer_id, earned_at);

CREATE INDEX affiliate_commissions_by_date ON affiliate_commissions USING btree (tenant_id, earned_at DESC, id);

CREATE INDEX affiliate_commissions_by_payout ON affiliate_commissions USING btree (tenant_id, payout_id) WHERE (payout_id IS NOT NULL);

CREATE INDEX affiliate_commissions_due ON affiliate_commissions USING btree (approve_after) WHERE (status = 'pending'::text);

CREATE UNIQUE INDEX affiliate_commissions_invoice ON affiliate_commissions USING btree (tenant_id, invoice_id) WHERE (kind = 'commission'::text);

CREATE INDEX affiliate_commissions_payable ON affiliate_commissions USING btree (tenant_id, affiliate_id, currency) WHERE ((status = 'approved'::text) AND (payout_id IS NULL));

CREATE UNIQUE INDEX affiliate_commissions_reversal_key ON affiliate_commissions USING btree (tenant_id, reversal_key) WHERE (reversal_key IS NOT NULL);

CREATE INDEX affiliate_journal_by_date ON affiliate_journal USING btree (tenant_id, posted_at DESC, id);

CREATE INDEX affiliate_journal_by_payout ON affiliate_journal USING btree (tenant_id, payout_id);

CREATE INDEX affiliate_payouts_by_affiliate ON affiliate_payouts USING btree (tenant_id, affiliate_id, created_at DESC);

CREATE INDEX affiliate_payouts_by_date ON affiliate_payouts USING btree (tenant_id, created_at DESC, id);

CREATE UNIQUE INDEX affiliate_payouts_one_open ON affiliate_payouts USING btree (tenant_id, affiliate_id, currency) WHERE (status = 'open'::text);

CREATE INDEX affiliate_reversals_by_commission ON affiliate_reversals USING btree (tenant_id, commission_id);

CREATE INDEX affiliates_by_created ON affiliates USING btree (tenant_id, created_at DESC, id);

CREATE INDEX affiliates_by_email ON affiliates USING btree (lower(email)) WHERE (user_id IS NULL);

CREATE INDEX affiliates_by_user ON affiliates USING btree (user_id) WHERE (user_id IS NOT NULL);

CREATE UNIQUE INDEX affiliates_code_key ON affiliates USING btree (upper(code));

CREATE UNIQUE INDEX affiliates_email_key ON affiliates USING btree (tenant_id, lower(email));

CREATE INDEX coupon_redemptions_by_code ON coupon_redemptions USING btree (tenant_id, promotion_code_id, customer_id) WHERE (promotion_code_id IS NOT NULL);

CREATE INDEX coupon_redemptions_by_coupon ON coupon_redemptions USING btree (tenant_id, coupon_id, created_at DESC, id);

CREATE INDEX coupon_redemptions_by_customer ON coupon_redemptions USING btree (tenant_id, customer_id) WHERE (customer_id IS NOT NULL);

CREATE UNIQUE INDEX coupon_redemptions_target ON coupon_redemptions USING btree (tenant_id, target_kind, target_id) WHERE (reversed_at IS NULL);

CREATE INDEX customer_coupons_pending ON customer_coupons USING btree (tenant_id, customer_id, created_at, id) WHERE (consumed_at IS NULL);

CREATE UNIQUE INDEX one_pending_change ON subscription_changes USING btree (tenant_id, subscription_id) WHERE (state = 'pending'::text);

CREATE UNIQUE INDEX one_started_migration ON provider_migrations USING btree (tenant_id, subscription_id) WHERE (state = 'started'::text);

CREATE INDEX promotion_codes_by_affiliate ON promotion_codes USING btree (tenant_id, affiliate_id) WHERE (affiliate_id IS NOT NULL);

CREATE INDEX promotion_codes_by_coupon ON promotion_codes USING btree (tenant_id, coupon_id, created_at DESC, id);

CREATE UNIQUE INDEX promotion_codes_code ON promotion_codes USING btree (tenant_id, upper(code));

CREATE INDEX subscription_adjustments_unbilled ON subscription_adjustments USING btree (created_at) WHERE (billed_at IS NULL);

CREATE INDEX subscription_changes_due ON subscription_changes USING btree (effective_at) WHERE (state = 'pending'::text);

CREATE UNIQUE INDEX subscription_provider_ref ON subscriptions USING btree (tenant_id, provider, provider_ref) WHERE (provider_ref IS NOT NULL);

CREATE INDEX subscriptions_fee_due ON subscriptions USING btree (fee_due_at) WHERE (fee_due_at IS NOT NULL);

CREATE INDEX webhook_deliveries_by_endpoint ON webhook_deliveries USING btree (tenant_id, endpoint_id, created_at DESC);

CREATE INDEX webhook_deliveries_due ON webhook_deliveries USING btree (next_attempt_at) WHERE (state = 'pending'::text);

CREATE TRIGGER affiliate_attributions_set_once BEFORE DELETE OR UPDATE ON affiliate_attributions FOR EACH ROW EXECUTE FUNCTION affiliate_attributions_immutable();

CREATE TRIGGER affiliate_journal_append_only BEFORE DELETE OR UPDATE ON affiliate_journal FOR EACH ROW EXECUTE FUNCTION affiliate_journal_append_only();

CREATE CONSTRAINT TRIGGER affiliate_journal_balanced AFTER INSERT ON affiliate_journal DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION affiliate_journal_balanced();

CREATE TRIGGER webhook_endpoints_tenant_check BEFORE INSERT OR UPDATE OF active ON webhook_endpoints FOR EACH ROW EXECUTE FUNCTION webhook_endpoints_check_tenant();

ALTER TABLE ONLY affiliate_attribution_rejections
    ADD CONSTRAINT affiliate_attribution_rejections_tenant_id_affiliate_id_fkey FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates(tenant_id, id);

ALTER TABLE ONLY affiliate_attribution_rejections
    ADD CONSTRAINT affiliate_attribution_rejections_tenant_id_customer_id_fkey FOREIGN KEY (tenant_id, customer_id) REFERENCES customers(tenant_id, id);

ALTER TABLE ONLY affiliate_attributions
    ADD CONSTRAINT affiliate_attributions_tenant_id_affiliate_id_fkey FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates(tenant_id, id);

ALTER TABLE ONLY affiliate_attributions
    ADD CONSTRAINT affiliate_attributions_tenant_id_customer_id_fkey FOREIGN KEY (tenant_id, customer_id) REFERENCES customers(tenant_id, id);

ALTER TABLE ONLY affiliate_clicks_daily
    ADD CONSTRAINT affiliate_clicks_daily_tenant_id_affiliate_id_fkey FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates(tenant_id, id);

ALTER TABLE ONLY affiliate_commissions
    ADD CONSTRAINT affiliate_commissions_payout_fkey FOREIGN KEY (tenant_id, payout_id) REFERENCES affiliate_payouts(tenant_id, id);

ALTER TABLE ONLY affiliate_commissions
    ADD CONSTRAINT affiliate_commissions_tenant_id_affiliate_id_fkey FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates(tenant_id, id);

ALTER TABLE ONLY affiliate_commissions
    ADD CONSTRAINT affiliate_commissions_tenant_id_customer_id_fkey FOREIGN KEY (tenant_id, customer_id) REFERENCES customers(tenant_id, id);

ALTER TABLE ONLY affiliate_commissions
    ADD CONSTRAINT affiliate_commissions_tenant_id_parent_id_fkey FOREIGN KEY (tenant_id, parent_id) REFERENCES affiliate_commissions(tenant_id, id);

ALTER TABLE ONLY affiliate_journal
    ADD CONSTRAINT affiliate_journal_tenant_id_affiliate_id_fkey FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates(tenant_id, id);

ALTER TABLE ONLY affiliate_journal
    ADD CONSTRAINT affiliate_journal_tenant_id_payout_id_fkey FOREIGN KEY (tenant_id, payout_id) REFERENCES affiliate_payouts(tenant_id, id);

ALTER TABLE ONLY affiliate_payouts
    ADD CONSTRAINT affiliate_payouts_tenant_id_affiliate_id_fkey FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates(tenant_id, id);

ALTER TABLE ONLY affiliate_programs
    ADD CONSTRAINT affiliate_programs_tenant_id_referred_coupon_id_fkey FOREIGN KEY (tenant_id, referred_coupon_id) REFERENCES coupons(tenant_id, id);

ALTER TABLE ONLY affiliate_reversals
    ADD CONSTRAINT affiliate_reversals_tenant_id_commission_id_fkey FOREIGN KEY (tenant_id, commission_id) REFERENCES affiliate_commissions(tenant_id, id);

ALTER TABLE ONLY coupon_redemptions
    ADD CONSTRAINT coupon_redemptions_tenant_id_coupon_id_fkey FOREIGN KEY (tenant_id, coupon_id) REFERENCES coupons(tenant_id, id);

ALTER TABLE ONLY coupon_redemptions
    ADD CONSTRAINT coupon_redemptions_tenant_id_promotion_code_id_fkey FOREIGN KEY (tenant_id, promotion_code_id) REFERENCES promotion_codes(tenant_id, id);

ALTER TABLE ONLY customer_coupons
    ADD CONSTRAINT customer_coupons_tenant_id_coupon_id_fkey FOREIGN KEY (tenant_id, coupon_id) REFERENCES coupons(tenant_id, id);

ALTER TABLE ONLY customer_coupons
    ADD CONSTRAINT customer_coupons_tenant_id_customer_id_fkey FOREIGN KEY (tenant_id, customer_id) REFERENCES customers(tenant_id, id);

ALTER TABLE ONLY customer_entitlement_overrides
    ADD CONSTRAINT customer_entitlement_overrides_tenant_id_customer_id_fkey FOREIGN KEY (tenant_id, customer_id) REFERENCES customers(tenant_id, id);

ALTER TABLE ONLY plan_currency_prices
    ADD CONSTRAINT plan_currency_prices_tenant_id_plan_version_id_fkey FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions(tenant_id, id);

ALTER TABLE ONLY plan_entitlements
    ADD CONSTRAINT plan_entitlements_tenant_id_plan_version_id_fkey FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions(tenant_id, id);

ALTER TABLE ONLY plan_prices
    ADD CONSTRAINT plan_prices_tenant_id_plan_version_id_fkey FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions(tenant_id, id);

ALTER TABLE ONLY plan_version_fees
    ADD CONSTRAINT plan_version_fees_tenant_id_plan_version_id_fkey FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions(tenant_id, id);

ALTER TABLE ONLY plan_versions
    ADD CONSTRAINT plan_versions_tenant_id_plan_id_fkey FOREIGN KEY (tenant_id, plan_id) REFERENCES plans(tenant_id, id);

ALTER TABLE ONLY promotion_codes
    ADD CONSTRAINT promotion_codes_affiliate_fkey FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates(tenant_id, id);

ALTER TABLE ONLY promotion_codes
    ADD CONSTRAINT promotion_codes_tenant_id_coupon_id_fkey FOREIGN KEY (tenant_id, coupon_id) REFERENCES coupons(tenant_id, id);

ALTER TABLE ONLY provider_migrations
    ADD CONSTRAINT provider_migrations_tenant_id_subscription_id_fkey FOREIGN KEY (tenant_id, subscription_id) REFERENCES subscriptions(tenant_id, id);

ALTER TABLE ONLY subscription_adjustments
    ADD CONSTRAINT subscription_adjustments_tenant_id_subscription_id_fkey FOREIGN KEY (tenant_id, subscription_id) REFERENCES subscriptions(tenant_id, id);

ALTER TABLE ONLY subscription_changes
    ADD CONSTRAINT subscription_changes_tenant_id_plan_version_id_fkey FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions(tenant_id, id);

ALTER TABLE ONLY subscription_changes
    ADD CONSTRAINT subscription_changes_tenant_id_subscription_id_fkey FOREIGN KEY (tenant_id, subscription_id) REFERENCES subscriptions(tenant_id, id);

ALTER TABLE ONLY subscription_coupons
    ADD CONSTRAINT subscription_coupons_tenant_id_coupon_code_fkey FOREIGN KEY (tenant_id, coupon_code) REFERENCES coupons(tenant_id, code);

ALTER TABLE ONLY subscription_coupons
    ADD CONSTRAINT subscription_coupons_tenant_id_subscription_id_fkey FOREIGN KEY (tenant_id, subscription_id) REFERENCES subscriptions(tenant_id, id);

ALTER TABLE ONLY subscription_trial_notices
    ADD CONSTRAINT subscription_trial_notices_tenant_id_customer_id_fkey FOREIGN KEY (tenant_id, customer_id) REFERENCES customers(tenant_id, id);

ALTER TABLE ONLY subscriptions
    ADD CONSTRAINT subscriptions_tenant_id_customer_id_fkey FOREIGN KEY (tenant_id, customer_id) REFERENCES customers(tenant_id, id);

ALTER TABLE ONLY subscriptions
    ADD CONSTRAINT subscriptions_tenant_id_plan_version_id_fkey FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions(tenant_id, id);

ALTER TABLE ONLY webhook_deliveries
    ADD CONSTRAINT webhook_deliveries_tenant_id_endpoint_id_fkey FOREIGN KEY (tenant_id, endpoint_id) REFERENCES webhook_endpoints(tenant_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY webhook_deliveries
    ADD CONSTRAINT webhook_deliveries_tenant_id_event_id_fkey FOREIGN KEY (tenant_id, event_id) REFERENCES webhook_events(tenant_id, id) ON DELETE CASCADE;

ALTER TABLE affiliate_attribution_rejections ENABLE ROW LEVEL SECURITY;

ALTER TABLE affiliate_attributions ENABLE ROW LEVEL SECURITY;

ALTER TABLE affiliate_clicks_daily ENABLE ROW LEVEL SECURITY;

ALTER TABLE affiliate_commissions ENABLE ROW LEVEL SECURITY;

ALTER TABLE affiliate_journal ENABLE ROW LEVEL SECURITY;

ALTER TABLE affiliate_payouts ENABLE ROW LEVEL SECURITY;

ALTER TABLE affiliate_programs ENABLE ROW LEVEL SECURITY;

ALTER TABLE affiliate_reversals ENABLE ROW LEVEL SECURITY;

ALTER TABLE affiliates ENABLE ROW LEVEL SECURITY;

ALTER TABLE coupon_redemptions ENABLE ROW LEVEL SECURITY;

ALTER TABLE coupons ENABLE ROW LEVEL SECURITY;

ALTER TABLE customer_coupons ENABLE ROW LEVEL SECURITY;

ALTER TABLE customer_entitlement_overrides ENABLE ROW LEVEL SECURITY;

ALTER TABLE customers ENABLE ROW LEVEL SECURITY;

ALTER TABLE meters ENABLE ROW LEVEL SECURITY;

ALTER TABLE plan_currency_prices ENABLE ROW LEVEL SECURITY;

ALTER TABLE plan_entitlements ENABLE ROW LEVEL SECURITY;

ALTER TABLE plan_prices ENABLE ROW LEVEL SECURITY;

ALTER TABLE plan_version_fees ENABLE ROW LEVEL SECURITY;

ALTER TABLE plan_versions ENABLE ROW LEVEL SECURITY;

ALTER TABLE plans ENABLE ROW LEVEL SECURITY;

ALTER TABLE promotion_codes ENABLE ROW LEVEL SECURITY;

ALTER TABLE provider_migrations ENABLE ROW LEVEL SECURITY;

ALTER TABLE routing_rules ENABLE ROW LEVEL SECURITY;

ALTER TABLE subscription_adjustments ENABLE ROW LEVEL SECURITY;

ALTER TABLE subscription_changes ENABLE ROW LEVEL SECURITY;

ALTER TABLE subscription_coupons ENABLE ROW LEVEL SECURITY;

ALTER TABLE subscription_trial_notices ENABLE ROW LEVEL SECURITY;

ALTER TABLE subscriptions ENABLE ROW LEVEL SECURITY;

ALTER TABLE tax_rates ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON affiliate_attribution_rejections USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON affiliate_attributions USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON affiliate_clicks_daily USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON affiliate_commissions USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON affiliate_journal USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON affiliate_payouts USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON affiliate_programs USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON affiliate_reversals USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON affiliates USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON coupon_redemptions USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON coupons USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON customer_coupons USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON customer_entitlement_overrides USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON customers USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON meters USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON plan_currency_prices USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON plan_entitlements USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON plan_prices USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON plan_version_fees USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON plan_versions USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON plans USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON promotion_codes USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON provider_migrations USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON routing_rules USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON subscription_adjustments USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON subscription_changes USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON subscription_coupons USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON subscription_trial_notices USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON subscriptions USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON tax_rates USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON tenant_mail_integrations USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON tenant_mail_settings USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON webhook_deliveries USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON webhook_endpoints USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

CREATE POLICY tenant_isolation ON webhook_events USING ((tenant_id = app_tenant_id())) WITH CHECK ((tenant_id = app_tenant_id()));

ALTER TABLE tenant_mail_integrations ENABLE ROW LEVEL SECURITY;

ALTER TABLE tenant_mail_settings ENABLE ROW LEVEL SECURITY;

ALTER TABLE webhook_deliveries ENABLE ROW LEVEL SECURITY;

ALTER TABLE webhook_endpoints ENABLE ROW LEVEL SECURITY;

ALTER TABLE webhook_events ENABLE ROW LEVEL SECURITY;

GRANT SELECT,INSERT ON TABLE affiliate_attribution_rejections TO plans_app;
GRANT SELECT,DELETE ON TABLE affiliate_attribution_rejections TO plans_system;

GRANT SELECT,INSERT ON TABLE affiliate_attributions TO plans_app;
GRANT SELECT,DELETE ON TABLE affiliate_attributions TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE affiliate_clicks_daily TO plans_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE affiliate_clicks_daily TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE affiliate_commissions TO plans_app;
GRANT SELECT,DELETE,UPDATE ON TABLE affiliate_commissions TO plans_system;

GRANT SELECT,INSERT ON TABLE affiliate_journal TO plans_app;
GRANT SELECT,DELETE ON TABLE affiliate_journal TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE affiliate_payouts TO plans_app;
GRANT SELECT,DELETE ON TABLE affiliate_payouts TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE affiliate_programs TO plans_app;
GRANT SELECT ON TABLE affiliate_programs TO plans_system;

GRANT SELECT,INSERT ON TABLE affiliate_reversals TO plans_app;
GRANT SELECT,DELETE ON TABLE affiliate_reversals TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE affiliates TO plans_app;
GRANT SELECT,INSERT,UPDATE ON TABLE affiliates TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE coupon_redemptions TO plans_app;
GRANT SELECT,DELETE ON TABLE coupon_redemptions TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE coupons TO plans_app;
GRANT SELECT ON TABLE coupons TO plans_system;

GRANT UPDATE(redeemed) ON TABLE coupons TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE customer_coupons TO plans_app;
GRANT SELECT,DELETE ON TABLE customer_coupons TO plans_system;

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE customer_entitlement_overrides TO plans_app;
GRANT SELECT,DELETE ON TABLE customer_entitlement_overrides TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE customers TO plans_app;
GRANT SELECT,DELETE ON TABLE customers TO plans_system;

GRANT UPDATE(address) ON TABLE customers TO plans_app;

GRANT UPDATE(tax_id) ON TABLE customers TO plans_app;

GRANT UPDATE(tax_office) ON TABLE customers TO plans_app;

GRANT UPDATE(e_invoice_registered) ON TABLE customers TO plans_app;

GRANT SELECT,INSERT ON TABLE meters TO plans_app;
GRANT SELECT ON TABLE meters TO plans_system;

GRANT INSERT ON TABLE outbox TO plans_app;
GRANT SELECT,DELETE ON TABLE outbox TO plans_system;

GRANT UPDATE(id) ON TABLE outbox TO plans_system;

GRANT SELECT,INSERT ON TABLE plan_currency_prices TO plans_app;
GRANT SELECT ON TABLE plan_currency_prices TO plans_system;

GRANT SELECT,INSERT ON TABLE plan_entitlements TO plans_app;
GRANT SELECT ON TABLE plan_entitlements TO plans_system;

GRANT SELECT,INSERT ON TABLE plan_prices TO plans_app;
GRANT SELECT ON TABLE plan_prices TO plans_system;

GRANT SELECT,INSERT ON TABLE plan_version_fees TO plans_app;
GRANT SELECT ON TABLE plan_version_fees TO plans_system;

GRANT SELECT,INSERT ON TABLE plan_versions TO plans_app;
GRANT SELECT ON TABLE plan_versions TO plans_system;

GRANT SELECT,INSERT ON TABLE plans TO plans_app;
GRANT SELECT ON TABLE plans TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE promotion_codes TO plans_app;
GRANT SELECT ON TABLE promotion_codes TO plans_system;

GRANT UPDATE(redeemed) ON TABLE promotion_codes TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE provider_migrations TO plans_app;
GRANT SELECT,DELETE ON TABLE provider_migrations TO plans_system;

GRANT SELECT,INSERT,DELETE ON TABLE routing_rules TO plans_app;
GRANT SELECT ON TABLE routing_rules TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE subscription_adjustments TO plans_app;
GRANT SELECT,DELETE ON TABLE subscription_adjustments TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE subscription_changes TO plans_app;
GRANT SELECT,DELETE ON TABLE subscription_changes TO plans_system;

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE subscription_coupons TO plans_app;
GRANT SELECT,DELETE ON TABLE subscription_coupons TO plans_system;

GRANT SELECT,INSERT ON TABLE subscription_trial_notices TO plans_app;
GRANT SELECT,DELETE ON TABLE subscription_trial_notices TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE subscriptions TO plans_app;
GRANT SELECT,DELETE ON TABLE subscriptions TO plans_system;

GRANT SELECT,INSERT,DELETE ON TABLE tax_rates TO plans_app;
GRANT SELECT ON TABLE tax_rates TO plans_system;

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE tenant_mail_integrations TO plans_app;
GRANT SELECT ON TABLE tenant_mail_integrations TO plans_system;

GRANT SELECT,INSERT,UPDATE ON TABLE tenant_mail_settings TO plans_app;
GRANT SELECT ON TABLE tenant_mail_settings TO plans_system;

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE webhook_deliveries TO plans_app;
GRANT SELECT,DELETE,UPDATE ON TABLE webhook_deliveries TO plans_system;

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE webhook_endpoints TO plans_app;
GRANT SELECT ON TABLE webhook_endpoints TO plans_system;

GRANT SELECT,INSERT ON TABLE webhook_events TO plans_app;
GRANT SELECT,DELETE ON TABLE webhook_events TO plans_system;

GRANT ALL ON FUNCTION app_tenant_id() TO plans_app;
GRANT ALL ON FUNCTION app_tenant_id() TO plans_system;
GRANT ALL ON FUNCTION current_tenant() TO plans_app;

-- +goose Down
DROP TABLE webhook_deliveries, webhook_events, webhook_endpoints, tenant_mail_integrations, tenant_mail_settings, tax_rates, subscription_trial_notices, subscription_coupons,
  subscription_changes, subscription_adjustments, provider_migrations, subscriptions, routing_rules, promotion_codes, plan_currency_prices, plan_entitlements, plan_prices,
  plan_version_fees, plan_versions, plans, outbox, meters, customer_entitlement_overrides, customer_coupons, coupon_redemptions, affiliate_reversals,
  affiliate_attribution_rejections, affiliate_attributions, affiliate_clicks_daily, affiliate_journal, affiliate_commissions, affiliate_payouts, affiliate_programs,
  affiliates, coupons, customers;
DROP FUNCTION webhook_endpoints_check_tenant(), affiliate_journal_balanced(), affiliate_journal_append_only(), affiliate_attributions_immutable(), current_tenant(), app_tenant_id();
DROP TYPE tenants;
