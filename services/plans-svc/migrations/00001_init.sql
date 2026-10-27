-- plans_db schema (docs/05-data-model.md#plans_db).
-- This is the baseline: it was written once, before the first release, from the schema the pre-release migrations had built, and it starts the history.
-- Every change from here on is a new numbered migration (00002_...), never an edit of this file.
-- Roles: plans_migrator owns everything; plans_app (request handlers, RLS enforced); plans_system (BYPASSRLS).

-- +goose Up
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Tenant of the current transaction, set by pgxx.InTenantTx. NULLIF: a pooled connection that saw a transaction-local
-- set_config returns '' afterwards, not NULL.
-- +goose StatementBegin
CREATE FUNCTION app_tenant_id() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE
AS $$ SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid $$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Accounts, users and apps
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- platform: one account is the platform's own (Payrudder as a SaaS that bills itself, ADR 0024): its tenant owns the plans Free, Pro and Scale, and every other account is
-- a customer of that tenant with a subscription. At most one platform account: two replicas bootstrapping at once cannot make two.
CREATE TABLE accounts (
  id          uuid PRIMARY KEY,
  name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  created_at  timestamptz NOT NULL DEFAULT now(),
  platform    boolean NOT NULL DEFAULT false
);
CREATE UNIQUE INDEX accounts_one_platform ON accounts ((true)) WHERE platform;

-- People. A user signs up, owns an account (organisation) and signs in with a session token. Users are not tenants: none of these tables
-- has a tenant_id and plans_app has no grant on them; only the identity module, on the system pool, reads and writes them.
CREATE TABLE users (
  id             uuid PRIMARY KEY,
  -- Trimmed and lower-cased by the module; the unique index below makes it case-insensitive even if a caller forgets.
  email          text NOT NULL CHECK (length(email) BETWEEN 3 AND 254),
  display_name   text NOT NULL DEFAULT '' CHECK (length(display_name) <= 200),
  -- PHC string of argon2id (parameters travel with the hash, so they can be raised later and old hashes still verify).
  password_hash  text NOT NULL,
  failed_logins  int NOT NULL DEFAULT 0 CHECK (failed_logins >= 0),
  locked_until   timestamptz,
  last_login_at  timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now(),
  -- A person's e-mail address is proven (verification) and a forgotten password can be replaced (reset); both use a single-use token mailed to the address.
  -- Set when the person opens the mailed link (or resets the password through the mailed link, which proves the same thing). NULL: not proven.
  email_verified_at timestamptz,
  -- The language the person last used, for the mails sent to them (en, tr, de, fr, es, ar).
  locale text NOT NULL DEFAULT 'en' CHECK (locale IN ('en', 'tr', 'de', 'fr', 'es', 'ar')),
  -- When the owner of an address was last told that somebody tried to sign up with it; at most one such mail an hour.
  taken_notice_at timestamptz
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

-- An account has apps, an app has a sandbox and a live tenant. The tenant stays the unit of isolation (RLS); the app groups the two environments of one
-- application, so a company can run several applications under one account, each with its own keys, customers, plans and invoices.
-- Apps can be renamed and deleted. Deleting never erases: an app holds invoices, payments and subscriptions that accounting and the retention rules keep, so
-- a deleted app is archived. It leaves every list of the account, its API keys are revoked, no key can be made for it again and its slug is freed for a new
-- app; its tenants, customers, invoices, payments and usage stay where they are. Only the operator API still lists it (with its archive time).
-- Both actions leave a row in app_audit (nothing in it is ever updated or deleted).
CREATE TABLE apps (
  id          uuid PRIMARY KEY,
  account_id  uuid NOT NULL REFERENCES accounts (id),
  name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  slug        text NOT NULL CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 60),
  created_at  timestamptz NOT NULL DEFAULT now(),
  archived_at timestamptz,
  archived_by uuid REFERENCES users (id),
  UNIQUE (account_id, slug),
  CONSTRAINT apps_archived_by_needs_time CHECK (archived_by IS NULL OR archived_at IS NOT NULL)
);
CREATE INDEX apps_live_by_account ON apps (account_id) WHERE archived_at IS NULL;

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- People: members, sessions, tokens, invitations and sign-in methods
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- role 'affiliate' is a partner who sees only their own data. Every account-wide read in identity leaves this role out.
CREATE TABLE account_members (
  account_id  uuid NOT NULL REFERENCES accounts (id),
  user_id     uuid NOT NULL REFERENCES users (id),
  role        text NOT NULL CHECK (role IN ('owner', 'admin', 'viewer', 'affiliate')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (account_id, user_id)
);
CREATE INDEX account_members_by_user ON account_members (user_id);

-- The token is opaque and random; only its SHA-256 is stored, so a database leak cannot be replayed as sessions.
CREATE TABLE user_sessions (
  id            uuid PRIMARY KEY,
  user_id       uuid NOT NULL REFERENCES users (id),
  token_hash    bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  revoked_at    timestamptz
);
CREATE INDEX user_sessions_by_user ON user_sessions (user_id);

-- Only the SHA-256 of the token is stored (the token carries 256 random bits): a leaked database holds no working link.
CREATE TABLE user_tokens (
  id          uuid PRIMARY KEY,
  user_id     uuid NOT NULL REFERENCES users (id),
  kind        text NOT NULL CHECK (kind IN ('verify_email', 'reset_password')),
  token_hash  bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz
);
CREATE INDEX user_tokens_by_user ON user_tokens (user_id, kind, created_at DESC);

-- Members join an account by invitation. An invitation is a single-use token mailed to an address; only its SHA-256 is stored.
CREATE TABLE account_invites (
  id          uuid PRIMARY KEY,
  account_id  uuid NOT NULL REFERENCES accounts (id),
  email       text NOT NULL CHECK (length(email) BETWEEN 3 AND 254),
  role        text NOT NULL CHECK (role IN ('owner', 'admin', 'viewer')),
  invited_by  uuid NOT NULL REFERENCES users (id),
  token_hash  bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  created_at  timestamptz NOT NULL DEFAULT now(),
  -- When the mail last went out (a resend moves it), for the one-a-minute rule.
  sent_at     timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  accepted_at timestamptz,
  revoked_at  timestamptz
);
-- One open invitation per address and account; a new invitation to the same address refreshes it instead of adding a second.
CREATE UNIQUE INDEX account_invites_open ON account_invites (account_id, lower(email)) WHERE accepted_at IS NULL AND revoked_at IS NULL;
CREATE INDEX account_invites_by_account ON account_invites (account_id, created_at DESC);

-- The three ways to sign in: e-mail + password, the one-time code (OTP, passwordless) and Google.
-- A one-time sign-in code mailed to an address. The row is keyed by the address, not by a user: an address without an account can sign up with a code. Only a
-- keyed hash of the code is stored (HMAC-SHA256 over salt, address and code, keyed with the server's pepper), the code has 6 digits, lives 10 minutes, works
-- once and is burned after 5 wrong tries.
CREATE TABLE login_codes (
  id          uuid PRIMARY KEY,
  email       text NOT NULL CHECK (length(email) BETWEEN 3 AND 254),
  salt        bytea NOT NULL CHECK (length(salt) = 16),
  code_hash   bytea NOT NULL CHECK (length(code_hash) = 32),
  attempts    int NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz
);
CREATE INDEX login_codes_by_email ON login_codes (email, created_at DESC);

-- A login at an identity provider, linked to a user: (provider, subject) is what the provider calls the person and never changes, unlike the address.
CREATE TABLE user_identities (
  id             uuid PRIMARY KEY,
  user_id        uuid NOT NULL REFERENCES users (id),
  provider       text NOT NULL CHECK (provider IN ('google')),
  subject        text NOT NULL CHECK (length(subject) BETWEEN 1 AND 255),
  -- The address the provider reported when the link was made; informational (the user's own address is users.email).
  email          text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  last_login_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (provider, subject)
);
CREATE INDEX user_identities_by_user ON user_identities (user_id);

-- What the console remembers for a person rather than for an account: how far they are in the first-run wizard, and which page tours they have seen. It used to
-- live in the browser's storage, so a second browser started the wizard and every tour again. Like the other person tables (users, user_sessions) it has no
-- tenant_id and plans_app has no grant on it: only the identity module, on the system pool, reads and writes it, always for the user the gateway resolved from
-- the session.
--
-- wizard is the progress document (status, step, what was chosen and made); seen_tours is a set of tour ids. Both are merged on write, never replaced: see
-- identity/onboarding.
CREATE TABLE user_onboarding (
  user_id     uuid PRIMARY KEY REFERENCES users (id),
  -- NULL: the wizard was never started. The shape is checked by the module; the database only keeps it an object.
  wizard      jsonb CHECK (jsonb_typeof(wizard) = 'object'),
  seen_tours  text[] NOT NULL DEFAULT '{}',
  updated_at  timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Tenants and API keys
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- invoice_logo_asset_id: the tenant's invoice logo is an assets-svc asset (invoice_logo purpose); the id is a plain reference, since assets live in another service's
-- database.
-- address: postal addresses. A customer's billing address and a tenant's (seller) address are printed on the invoice PDF and the hosted invoice page. Stored as one
-- JSON object ({line1, line2, city, region, postal_code, country}); nothing queries inside it.
-- bank_transfer: the public part of a tenant's bank transfer account (bank name, account holder, IBAN, BIC, reference template, currencies) is printed on its invoices.
-- The account is stored sealed in payments-worker; it copies this part here, and billing-svc freezes it into the invoices it makes. One JSON object; nothing queries
-- inside it.
-- legal, e_invoice_on: the data an e-invoice needs. The seller's company data and when an invoice becomes an e-invoice live on the tenant.
-- status: a live tenant is created `inactive` and has no keys until the go-live checklist is complete (owner e-mail verified, legal profile, a live payment provider, a
-- published plan, terms accepted). A sandbox tenant is always active. The status is always chosen by the code that creates a tenant, so it has no default.
-- cell, region, placement_status: the tenant directory (ADR 0018). A tenant row says where the tenant's data lives: cell '' is the pooled database (row-level security,
-- the default mode); any other value names a cell, a Postgres cluster with one schema per tenant. region is where the cell is (informational, for the operator);
-- placement_status is the fence of a move ('moving' refuses every request and job of the tenant).
CREATE TABLE tenants (
  id            uuid PRIMARY KEY,
  account_id    uuid NOT NULL REFERENCES accounts (id),
  environment   text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  name          text NOT NULL,
  tier          text NOT NULL CHECK (tier IN ('free', 'standard', 'enterprise')),
  currency      char(3) NOT NULL,
  timezone      text NOT NULL,
  grace_period  interval NOT NULL CHECK (grace_period BETWEEN interval '0' AND interval '7 days'),
  created_at    timestamptz NOT NULL DEFAULT now(),
  invoice_logo_asset_id uuid,
  app_id        uuid NOT NULL REFERENCES apps (id),
  address       jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(address) = 'object'),
  bank_transfer jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(bank_transfer) = 'object'),
  legal         jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(legal) = 'object'),
  e_invoice_on  text NOT NULL DEFAULT 'off' CHECK (e_invoice_on IN ('off', 'finalized', 'paid')),
  status        text NOT NULL CHECK (status IN ('inactive', 'active', 'suspended')),
  activated_at  timestamptz,
  terms_accepted_at timestamptz,
  terms_accepted_by uuid REFERENCES users (id),
  cell          text NOT NULL DEFAULT '' CHECK (cell = '' OR cell ~ '^[a-z0-9][a-z0-9-]{0,30}$'),
  region        text NOT NULL DEFAULT '' CHECK (region = '' OR region ~ '^[a-z0-9][a-z0-9-]{0,30}$'),
  placement_status text NOT NULL DEFAULT 'ready' CHECK (placement_status IN ('ready', 'moving')),
  CONSTRAINT tenants_app_id_environment_key UNIQUE (app_id, environment),
  CONSTRAINT sandbox_is_always_active CHECK (environment = 'live' OR status = 'active')
);
CREATE INDEX tenants_by_account ON tenants (account_id);
-- Cells hold few tenants; the operator lists them by cell.
CREATE INDEX tenants_cell ON tenants (cell) WHERE cell <> '';

-- +goose StatementBegin
CREATE FUNCTION tenants_immutable_settings() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.currency IS DISTINCT FROM OLD.currency OR NEW.timezone IS DISTINCT FROM OLD.timezone
     OR NEW.environment IS DISTINCT FROM OLD.environment OR NEW.account_id IS DISTINCT FROM OLD.account_id
     OR NEW.app_id IS DISTINCT FROM OLD.app_id THEN
    RAISE EXCEPTION 'tenant currency, timezone, environment, account and app are immutable'
      USING ERRCODE = 'P0001', HINT = 'tenant_immutable';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER tenants_immutable BEFORE UPDATE ON tenants
  FOR EACH ROW EXECUTE FUNCTION tenants_immutable_settings();

-- The app role reads its own tenant row through this function only (tenants itself is operator-only).
-- +goose StatementBegin
CREATE FUNCTION current_tenant() RETURNS SETOF tenants
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
AS $$ SELECT * FROM tenants WHERE id = app_tenant_id() $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION current_tenant() FROM PUBLIC;

-- An API key has a role and the permissions it was issued with.
-- The role names a set (admin, billing, read_only, ingest_only) or says the permissions were chosen one by one (custom). The permissions are what the
-- gateway enforces, resource:action, from the vocabulary in pkg/apikey. A permission added later reaches the admin keys that already exist through the gateway,
-- which treats the role admin as holding every permission. last_used_at and expires_at: the old key of a rotation stays valid for an overlap window.
CREATE TABLE api_keys (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants (id),
  prefix      char(8) NOT NULL UNIQUE,
  key_hash    bytea NOT NULL CHECK (length(key_hash) = 32),
  env         text NOT NULL CHECK (env IN ('live', 'test')),
  status      text NOT NULL CHECK (status IN ('active', 'revoked')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  revoked_at  timestamptz,
  last_used_at timestamptz,
  expires_at  timestamptz,
  role        text   NOT NULL DEFAULT 'admin' CHECK (role IN ('admin', 'billing', 'read_only', 'ingest_only', 'custom')),
  permissions text[] NOT NULL DEFAULT '{}',
  CONSTRAINT api_keys_permissions_present CHECK (role = 'admin' OR cardinality(permissions) > 0)
);
CREATE INDEX api_keys_tenant ON api_keys (tenant_id);

-- The last line of defence: a key whose environment disagrees with its tenant's, or a live key of a tenant that is not active, cannot be written whatever the
-- code above did.
-- +goose StatementBegin
CREATE FUNCTION api_keys_check_tenant() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  t tenants;
BEGIN
  SELECT * INTO t FROM tenants WHERE id = NEW.tenant_id;
  IF (t.environment = 'sandbox') <> (NEW.env = 'test') THEN
    RAISE EXCEPTION 'an api key environment must match its tenant' USING ERRCODE = 'P0001', HINT = 'key_environment';
  END IF;
  IF t.environment = 'live' AND t.status <> 'active' AND NEW.status = 'active' THEN
    RAISE EXCEPTION 'a live tenant needs to be active to have keys' USING ERRCODE = 'P0001', HINT = 'tenant_inactive';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER api_keys_tenant_check BEFORE INSERT ON api_keys FOR EACH ROW EXECUTE FUNCTION api_keys_check_tenant();

-- The last line of defence, as for a live tenant that is not active: an active key of an archived app cannot be written whatever the code above did.
-- +goose StatementBegin
CREATE FUNCTION api_keys_check_app_not_archived() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'active' AND EXISTS (
    SELECT 1 FROM tenants t JOIN apps a ON a.id = t.app_id WHERE t.id = NEW.tenant_id AND a.archived_at IS NOT NULL
  ) THEN
    RAISE EXCEPTION 'an archived app has no active keys' USING ERRCODE = 'P0001', HINT = 'app_archived';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER api_keys_app_not_archived BEFORE INSERT OR UPDATE OF status ON api_keys
  FOR EACH ROW EXECUTE FUNCTION api_keys_check_app_not_archived();

CREATE TABLE app_audit (
  id          uuid PRIMARY KEY,
  app_id      uuid NOT NULL REFERENCES apps (id),
  user_id     uuid NOT NULL REFERENCES users (id),
  action      text NOT NULL CHECK (action IN ('renamed', 'archived')),
  detail      jsonb NOT NULL DEFAULT '{}'::jsonb,   -- renamed: the old and the new name; archived: how many keys were revoked
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX app_audit_by_app ON app_audit (app_id, created_at DESC);

-- The audit of promoting an app's sandbox configuration to its live environment. One row per applied promotion (a dry run writes nothing).
-- Promotion goes one way only, and the database says so: the source must be the app's sandbox tenant and the target its live tenant.
CREATE TABLE config_promotions (
  id                uuid PRIMARY KEY,
  app_id            uuid NOT NULL REFERENCES apps (id),
  user_id           uuid NOT NULL REFERENCES users (id),
  sandbox_tenant_id uuid NOT NULL REFERENCES tenants (id),
  live_tenant_id    uuid NOT NULL REFERENCES tenants (id),
  created           int NOT NULL CHECK (created >= 0),
  unchanged         int NOT NULL CHECK (unchanged >= 0),
  conflicts         int NOT NULL CHECK (conflicts >= 0),
  skipped           int NOT NULL CHECK (skipped >= 0),
  failed            int NOT NULL CHECK (failed >= 0),
  detail            jsonb NOT NULL,                    -- the changes, without secrets
  created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX config_promotions_by_app ON config_promotions (app_id, created_at DESC);

-- +goose StatementBegin
CREATE FUNCTION config_promotions_one_way() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  s tenants;
  l tenants;
BEGIN
  SELECT * INTO s FROM tenants WHERE id = NEW.sandbox_tenant_id;
  SELECT * INTO l FROM tenants WHERE id = NEW.live_tenant_id;
  IF s.environment <> 'sandbox' OR l.environment <> 'live' OR s.app_id <> NEW.app_id OR l.app_id <> NEW.app_id THEN
    RAISE EXCEPTION 'configuration is promoted from the sandbox to the live environment of one app, and only that way' USING ERRCODE = 'P0001', HINT = 'promotion_direction';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER config_promotions_direction BEFORE INSERT ON config_promotions FOR EACH ROW EXECUTE FUNCTION config_promotions_one_way();

-- The audit trail of what an owner does on the way to going live (the seller's legal and billing profile, the terms of service). One row per change;
-- nothing is ever updated or deleted. The provider connection is audited in payments-worker (provider_account_events), next to the credentials it is about.
-- The row names the app's live tenant and the database says so: the profile of a sandbox environment is not part of going live.
CREATE TABLE go_live_audit (
  id              uuid PRIMARY KEY,
  app_id          uuid NOT NULL REFERENCES apps (id),
  live_tenant_id  uuid NOT NULL REFERENCES tenants (id),
  user_id         uuid NOT NULL REFERENCES users (id),
  action          text NOT NULL CHECK (action IN ('billing_profile_saved', 'terms_accepted')),
  detail          jsonb NOT NULL DEFAULT '{}'::jsonb,   -- field names and the country, never the values of the profile
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX go_live_audit_by_app ON go_live_audit (app_id, created_at DESC);

-- +goose StatementBegin
CREATE FUNCTION go_live_audit_live_only() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  l tenants;
BEGIN
  SELECT * INTO l FROM tenants WHERE id = NEW.live_tenant_id;
  IF l.environment <> 'live' OR l.app_id <> NEW.app_id THEN
    RAISE EXCEPTION 'the go-live audit is about the live environment of the app' USING ERRCODE = 'P0001', HINT = 'go_live_audit_environment';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER go_live_audit_live BEFORE INSERT ON go_live_audit FOR EACH ROW EXECUTE FUNCTION go_live_audit_live_only();

-- Resetting a sandbox. plans-svc owns the job; each service deletes its own part (SandboxResetService). Here: the job table, and (in the grants below) the right of
-- plans_system to delete what a reset deletes from plans_db (the customers' side: customers, subscriptions and their history, webhook history), nothing of the configuration.
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

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Customers, meters and plans
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- country and billing_currency: customers carry a country and a billing currency (routing and tax read the country; it is kept equal to the address's country).
-- tax_id, tax_office, e_invoice_registered: the buyer's tax id, tax office and e-fatura registration flag (an e-invoice needs them). Nothing here is checked against
-- the tax authority.
CREATE TABLE customers (
  tenant_id       uuid NOT NULL,
  id              uuid NOT NULL,
  external_id     text NOT NULL CHECK (length(external_id) BETWEEN 1 AND 128),
  name            text NOT NULL,
  email           text NOT NULL,
  payment_method  text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  country           char(2)  CHECK (country ~ '^[A-Z]{2}$'),
  billing_currency  char(3)  CHECK (billing_currency ~ '^[A-Z]{3}$'),
  address         jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(address) = 'object'),
  tax_id          text NOT NULL DEFAULT '',
  tax_office      text NOT NULL DEFAULT '',
  e_invoice_registered boolean NOT NULL DEFAULT false,
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, external_id)
);

-- category files a meter under a category of the tenant's own.
CREATE TABLE meters (
  tenant_id       uuid NOT NULL,
  code            text NOT NULL CHECK (code ~ '^[a-z][a-z0-9_]{0,62}$'),
  version         int  NOT NULL CHECK (version > 0),
  aggregation     text NOT NULL CHECK (aggregation IN ('SUM', 'COUNT', 'MAX', 'UNIQUE', 'LAST')),
  value_property  text,
  filter          jsonb NOT NULL DEFAULT '{}',
  quantity_scale  smallint NOT NULL CHECK (quantity_scale BETWEEN 0 AND 6),
  unit_label      text NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  category        text NOT NULL DEFAULT '' CHECK (length(category) <= 64),
  PRIMARY KEY (tenant_id, code, version),
  CHECK ((aggregation = 'COUNT') = (value_property IS NULL))
);
-- UNIQUE counts the distinct values of value_property per period; LAST is the quantity of the latest event of the period. The rule that value_property is empty
-- exactly for COUNT fits both (they name a property).

CREATE TABLE plans (
  tenant_id   uuid NOT NULL,
  id          uuid NOT NULL,
  code        text NOT NULL CHECK (code ~ '^[a-z][a-z0-9_-]{0,62}$'),
  name        text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, code)
);

-- trial_days: a plan version can carry a default trial: a subscription created on it without saying otherwise starts trialing for this many days (no card, no fee until
-- the trial ends). 0 is no trial.
CREATE TABLE plan_versions (
  tenant_id       uuid NOT NULL,
  id              uuid NOT NULL,
  plan_id         uuid NOT NULL,
  version         int  NOT NULL CHECK (version > 0),
  base_fee_minor  bigint NOT NULL DEFAULT 0 CHECK (base_fee_minor >= 0),
  published_at    timestamptz NOT NULL DEFAULT now(),
  trial_days      int NOT NULL DEFAULT 0 CHECK (trial_days BETWEEN 0 AND 365),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, plan_id, version),
  FOREIGN KEY (tenant_id, plan_id) REFERENCES plans (tenant_id, id)
);

-- A plan version's fixed fee is per interval and currency. The monthly fee in the tenant's currency stays in plan_versions.base_fee_minor;
-- every other combination (a yearly fee in any currency, a monthly fee in another currency) is a plan_version_fees row.
CREATE TABLE plan_version_fees (
  tenant_id        uuid NOT NULL,
  plan_version_id  uuid NOT NULL,
  currency         char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  base_fee_minor   bigint  NOT NULL CHECK (base_fee_minor >= 0),
  interval         text NOT NULL DEFAULT 'month' CHECK (interval IN ('month', 'year')),
  PRIMARY KEY (tenant_id, plan_version_id, currency, interval),
  FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions (tenant_id, id)
);

CREATE TABLE plan_prices (
  tenant_id        uuid NOT NULL,
  plan_version_id  uuid NOT NULL,
  meter_code       text NOT NULL,
  model            text NOT NULL CHECK (model IN ('PER_UNIT', 'TIERED_GRADUATED', 'PACKAGE')),
  config           jsonb NOT NULL,   -- validated in Go (internal/domain); quantities stored as scaled units. The included quantity of a price lives here too.
  PRIMARY KEY (tenant_id, plan_version_id, meter_code),
  FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions (tenant_id, id)
);

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

-- Quotas: an entitlement counts over a window (the billing month, or an hour, a day, an ISO week, a calendar year of twelve billing periods, or the customer's
-- lifetime) with an enforcement mode.
CREATE TABLE plan_entitlements (
  tenant_id             uuid NOT NULL,
  plan_version_id       uuid NOT NULL,
  meter_code            text NOT NULL,
  limit_units           bigint NOT NULL CHECK (limit_units >= 0),   -- scaled units; 0 = unlimited
  alert_thresholds_pct  int[]  NOT NULL DEFAULT '{80,100}',
  quota_window          text NOT NULL DEFAULT 'month' CHECK (quota_window IN ('hour', 'day', 'week', 'month', 'year', 'lifetime')),
  mode                  text NOT NULL DEFAULT 'hard'  CHECK (mode IN ('hard', 'soft', 'notify')),
  PRIMARY KEY (tenant_id, plan_version_id, meter_code),
  FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions (tenant_id, id)
);

-- Likewise, one customer can have its own limit on a meter instead of its plan version's.
CREATE TABLE customer_entitlement_overrides (
  tenant_id             uuid NOT NULL,
  customer_id           uuid NOT NULL,
  meter_code            text NOT NULL,
  limit_units           bigint NOT NULL CHECK (limit_units >= 0),   -- scaled units of the meter; 0 = unlimited
  alert_thresholds_pct  int[]  NOT NULL DEFAULT '{80,100}',
  quota_window          text NOT NULL DEFAULT 'month' CHECK (quota_window IN ('hour', 'day', 'week', 'month', 'year', 'lifetime')),
  mode                  text NOT NULL DEFAULT 'hard'  CHECK (mode IN ('hard', 'soft', 'notify')),
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, customer_id, meter_code),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id)
);

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Subscriptions
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- A subscription has a lifecycle, a currency, an optional provider and a quantity.
-- provider, provider_ref, owner: a subscription can be sold and run by a payment provider (Paddle). The provider is chosen when it is created (chosen by country and
-- currency); who owns renewal and dunning follows from the provider's capabilities; the provider's id of the subscription is linked when the provider tells us it
-- exists. owner is who renews a subscription and retries its failed renewals: `payrudder` or `provider`.
-- fee_timing: 'arrears' bills the fixed fee with the usage at the close of the calendar month, 'advance' invoices it at the start of each billing period. fee_due_at
-- is the start of the next period to invoice in advance (NULL for arrears subscriptions and for ones a provider bills); billing-svc advances it after the invoice exists.
-- quantity (seats, licences): a plain multiplier of the plan's fixed fee. Usage prices are not multiplied.
CREATE TABLE subscriptions (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  customer_id      uuid NOT NULL,
  plan_version_id  uuid NOT NULL,
  active           tstzrange NOT NULL CHECK (NOT isempty(active) AND lower(active) IS NOT NULL),
  created_at       timestamptz NOT NULL DEFAULT now(),
  state                text NOT NULL DEFAULT 'active'
    CHECK (state IN ('trialing', 'active', 'past_due', 'paused', 'cancelled')),
  interval             text NOT NULL DEFAULT 'month' CHECK (interval IN ('month', 'year')),
  currency             char(3) NOT NULL,
  trial_ends_at        timestamptz,
  cancel_at_period_end boolean NOT NULL DEFAULT false,
  paused_at            timestamptz,
  billing_anchor       timestamptz NOT NULL,    -- start of the calendar month (tenant time zone) the subscription began in; a plan change keeps it
  changed_from_id      uuid,                    -- the subscription this one replaced in a plan change
  provider           text CHECK (provider ~ '^[a-z][a-z0-9_-]{0,31}$'),
  provider_ref       text,
  owner              text NOT NULL DEFAULT 'payrudder' CHECK (owner IN ('payrudder', 'provider')),
  provider_synced_at timestamptz,
  fee_timing text NOT NULL DEFAULT 'arrears' CHECK (fee_timing IN ('advance', 'arrears')),
  fee_due_at timestamptz,
  quantity integer NOT NULL DEFAULT 1 CHECK (quantity BETWEEN 1 AND 100000),
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
  FOREIGN KEY (tenant_id, plan_version_id) REFERENCES plan_versions (tenant_id, id),
  CONSTRAINT one_active_subscription
    EXCLUDE USING gist (tenant_id WITH =, customer_id WITH =, active WITH &&),
  -- A provider-owned subscription has a provider and its id there.
  CONSTRAINT provider_owned_has_provider CHECK (owner = 'payrudder' OR (provider IS NOT NULL AND provider_ref IS NOT NULL))
);
CREATE UNIQUE INDEX subscription_provider_ref ON subscriptions (tenant_id, provider, provider_ref) WHERE provider_ref IS NOT NULL;
CREATE INDEX subscriptions_fee_due ON subscriptions (fee_due_at) WHERE fee_due_at IS NOT NULL;

-- Plan and interval changes (docs/15-billing-lifecycle.md). A change that waits for the end of the paid period is a pending
-- row here until billing's tick applies it; a change that moved money leaves an adjustment for billing-svc to invoice or credit.
-- quantity: a scheduled change may carry the quantity it moves to; NULL keeps the subscription's own.
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
  quantity            integer CHECK (quantity BETWEEN 1 AND 100000),
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

-- Moving a subscription from one payment provider to another (docs/14-billing-platform.md#provider-migration).
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

-- A trialing subscription is reminded once, a few days before its trial ends (subscription.trial_will_end webhook event, and an e-mail to the customer). This row
-- is the "already reminded" marker. It is keyed by the customer and the end of the trial, not by the subscription: a plan change during a trial replaces the
-- subscription but keeps the trial end, and must not remind again; a trial that is extended or started afresh has another end and is reminded again.
-- The job inserts the row in the same transaction that queues the event and the mail, and ON CONFLICT DO NOTHING makes a second instance, or a second pass, a no-op.
CREATE TABLE subscription_trial_notices (
  tenant_id        uuid NOT NULL,
  customer_id      uuid NOT NULL,
  trial_ends_at    timestamptz NOT NULL,
  subscription_id  uuid NOT NULL,
  notified_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, customer_id, trial_ends_at),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id)
);

-- Which payment provider a new subscription goes to, chosen from the customer's country and the subscription's currency.
CREATE TABLE routing_rules (
  tenant_id  uuid NOT NULL,
  position   int  NOT NULL CHECK (position >= 0),
  country    char(2) CHECK (country ~ '^[A-Z]{2}$'),
  currency   char(3) CHECK (currency ~ '^[A-Z]{3}$'),
  provider   text NOT NULL CHECK (provider ~ '^[a-z][a-z0-9_-]{0,31}$'),
  PRIMARY KEY (tenant_id, position)
);

-- The tax rate the platform adds to the invoices it raises, by the customer's country. The tenant sets the rates and is responsible for them; the platform applies
-- them as given (exclusive of the price) and does not work out what is due where. An empty country is the rate for every country without its own.
CREATE TABLE tax_rates (
  tenant_id uuid NOT NULL,
  country   text NOT NULL CHECK (country = '' OR country ~ '^[A-Z]{2}$'),
  name      text NOT NULL CHECK (length(name) BETWEEN 1 AND 40),
  rate_bps  int  NOT NULL CHECK (rate_bps BETWEEN 0 AND 10000),   -- 2000 = 20 %
  PRIMARY KEY (tenant_id, country)
);

-- A tenant can switch the platform's outgoing e-mail off per category (invoice issued, payment receipts, payment failed, usage alerts, system).
-- No row means every category is on; the row holds the categories that are off. Notifier reads it before it sends (a disabled category is dropped and counted).
CREATE TABLE tenant_mail_settings (
  tenant_id   uuid PRIMARY KEY,
  disabled    text[] NOT NULL DEFAULT '{}'
              CHECK (disabled <@ ARRAY['invoice_issued', 'payment_receipts', 'payment_failed', 'usage_alerts', 'system']::text[]),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

-- A live tenant can send its customer mail through its own e-mail provider (Nafru Email, which speaks the Resend API) instead of Payrudder's
-- built-in sender. One row per tenant. The API key is sealed (pkg/secretbox, the tenant id as associated data); only the last four characters are kept in the
-- clear, to tell which key is stored. The notifier reads the connection through plans-svc (ResolveMailSender) before it sends.
CREATE TABLE tenant_mail_integrations (
  tenant_id      uuid PRIMARY KEY,
  provider       text NOT NULL CHECK (provider IN ('nafru')),
  base_url       text NOT NULL CHECK (base_url <> ''),
  api_key_enc    bytea NOT NULL,
  key_hint       text NOT NULL DEFAULT '',
  from_address   text NOT NULL CHECK (from_address <> ''),
  checked_at     timestamptz,
  check_problem  text NOT NULL DEFAULT '' CHECK (check_problem IN ('', 'unreachable', 'refused')),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

-- relay of plans.entitlements.v1 (pkg/outbox expects ingested_at)
CREATE TABLE outbox (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id      uuid NOT NULL,
  topic          text NOT NULL,
  partition_key  text NOT NULL,
  payload        bytea,
  headers        jsonb NOT NULL DEFAULT '{}',
  ingested_at    timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Coupons, promotion codes and redemptions
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- A coupon is the definition of a discount (what it takes off): a percentage or an amount off the fixed fee of a subscription the platform renews, once, for N periods or
-- for as long as the subscription lasts, or bonus credits on a credit purchase. It can be used for a subscription, a credit purchase, a payment link or an invoice
-- (applies_to). One coupon per subscription; a plan change carries it over.
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
  id                uuid NOT NULL DEFAULT gen_random_uuid(),
  bonus_percent_bps int  CHECK (bonus_percent_bps BETWEEN 1 AND 100000),    -- extra credits as a share of the pack's: 1000 = 10 % more
  applies_to        text[] NOT NULL DEFAULT ARRAY['subscription', 'credit_purchase', 'payment']
    CHECK (cardinality(applies_to) >= 1 AND applies_to <@ ARRAY['subscription', 'credit_purchase', 'payment']),
  PRIMARY KEY (tenant_id, code),
  CONSTRAINT coupons_id_key UNIQUE (tenant_id, id),
  CONSTRAINT coupon_is_percent_amount_or_bonus CHECK (num_nonnulls(percent_bps, amount_minor, bonus_percent_bps) = 1),
  CONSTRAINT bonus_coupon_is_for_credits CHECK (bonus_percent_bps IS NULL OR applies_to = ARRAY['credit_purchase']),
  CONSTRAINT amount_coupon_has_currency CHECK (amount_minor IS NULL OR currency IS NOT NULL),
  CONSTRAINT repeating_has_periods CHECK ((duration = 'repeating') = (duration_periods IS NOT NULL))
);

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Affiliate program
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- The affiliate program (partners, codes, clicks, first-touch attribution).
--
-- Everything is tenant data, so a sandbox and a live environment have separate programs. A tenant has at most one program (one row); its affiliates are partners
-- who bring customers with a link or a code. A click is counted, never recorded: one row per affiliate per day, no visitor, no address. An attribution says which
-- affiliate brought a customer; it is written once and never changes (first touch wins), and the database refuses an UPDATE or DELETE of it.
--
-- The code of an affiliate is unique across the platform, not just within its tenant: the public link /r/<code> carries no tenant, and the code is what finds it.
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

-- A promotion code is what a customer types: many codes point to one coupon, each with its own cap, expiry and restrictions. affiliate_id: a promotion code that
-- belongs to an affiliate; a customer who redeems it is attributed to that affiliate.
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
  affiliate_id        uuid,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, coupon_id) REFERENCES coupons (tenant_id, id),
  CONSTRAINT promotion_codes_affiliate_fkey FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates (tenant_id, id),
  CONSTRAINT min_amount_has_currency CHECK ((min_amount_minor IS NULL) = (min_amount_currency IS NULL))
);
-- A customer types a code in any case; two codes that differ only by case would be one.
CREATE UNIQUE INDEX promotion_codes_code ON promotion_codes (tenant_id, upper(code));
CREATE INDEX promotion_codes_by_coupon ON promotion_codes (tenant_id, coupon_id, created_at DESC, id);
CREATE INDEX promotion_codes_by_affiliate ON promotion_codes (tenant_id, affiliate_id) WHERE affiliate_id IS NOT NULL;

-- Every redemption is a row of coupon_redemptions, claimed with a conditional update so two checkouts cannot both take the last one.
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

-- A coupon waiting for a customer (a referral programme gives one to the customer it brought); the next eligible checkout without a typed code uses it.
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

CREATE TABLE subscription_coupons (
  tenant_id       uuid NOT NULL,
  subscription_id uuid NOT NULL,
  coupon_code     text NOT NULL,
  redeemed_at     timestamptz NOT NULL DEFAULT now(),
  periods_applied int  NOT NULL DEFAULT 0,
  redemption_id   uuid,
  PRIMARY KEY (tenant_id, subscription_id),
  FOREIGN KEY (tenant_id, subscription_id) REFERENCES subscriptions (tenant_id, id),
  FOREIGN KEY (tenant_id, coupon_code) REFERENCES coupons (tenant_id, code)
);

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

-- First touch wins: nothing moves an attribution afterwards, whatever the code above does. A reset of a sandbox deletes the customers it names, so a DELETE is allowed
-- there, for a sandbox tenant only: UPDATE stays refused everywhere, and so does any DELETE in a live tenant that is still in the pooled database. The DELETE is also
-- allowed once the tenant has a cell (tenants.cell <> ''): that is the last step of moving the tenant out of the pooled database, where the directory already points at
-- the cell and the old rows are retired (the cells' own copy of this function, in migrations/tenant, reads the same facts from the tenant's row setting).
-- +goose StatementBegin
CREATE FUNCTION affiliate_attributions_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND EXISTS (SELECT 1 FROM tenants WHERE id = OLD.tenant_id AND (environment = 'sandbox' OR cell <> '')) THEN
    RETURN OLD;
  END IF;
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

-- What an affiliate earns, and the payouts and the books.
--
-- A payout is a batch of one affiliate's approved commissions in one currency (no conversion, ever): the positive ones and the negative rows that net earlier
-- payments taken back. It is `open` until the owner has paid it (by hand or bank transfer; Payrudder moves no money) and says so; then it is `paid`, its
-- commissions become `paid`, and the journal gets the postings: the commission expense and the payable, then the payable settled against cash. Each posting set
-- balances (debits = credits) in its currency, and the database refuses to commit one that does not.
--
-- A payout of a sandbox tenant is fake: it moves nothing, is marked so, and its postings carry the mark.
--
-- Resetting a sandbox empties the customers' side of it, the payouts and their postings included, since they describe commissions that are gone. The system role
-- may delete them for that, and a trigger lets it only for a sandbox (or a tenant that has moved to a cell, see above): the journal of a live tenant is never rewritten.
CREATE TABLE affiliate_payouts (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL DEFAULT gen_random_uuid(),
  affiliate_id     uuid NOT NULL,
  currency         char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  -- Set when the payout is made, and again when it is paid (what the rows came to then). While it is open the rows say what it is worth now.
  total_minor      bigint NOT NULL,
  commission_count int NOT NULL CHECK (commission_count >= 0),
  method           text NOT NULL CHECK (method IN ('manual', 'bank_transfer')),
  payout_details   text NOT NULL DEFAULT '',
  reference        text NOT NULL DEFAULT '' CHECK (length(reference) <= 200),
  status           text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'paid', 'cancelled')),
  sandbox          boolean NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  paid_at          timestamptz,
  cancelled_at     timestamptz,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates (tenant_id, id),
  CONSTRAINT payout_state CHECK ((status = 'paid') = (paid_at IS NOT NULL) AND (status = 'cancelled') = (cancelled_at IS NOT NULL)),
  CONSTRAINT paid_payout_is_positive CHECK (status <> 'paid' OR total_minor > 0)
);
-- One open payout per affiliate and currency: what is approved later goes in the next one.
CREATE UNIQUE INDEX affiliate_payouts_one_open ON affiliate_payouts (tenant_id, affiliate_id, currency) WHERE status = 'open';
CREATE INDEX affiliate_payouts_by_date ON affiliate_payouts (tenant_id, created_at DESC, id);
CREATE INDEX affiliate_payouts_by_affiliate ON affiliate_payouts (tenant_id, affiliate_id, created_at DESC);

-- One commission per paid invoice of an attributed customer (invoice_id is unique, so replaying invoice.paid creates one). It waits as `pending` for the program's
-- hold, the refund window, then a job makes it `approved`; a payout makes it `paid`. A refund, void, credit note or chargeback takes the commission back:
-- before it is paid that is a number on the commission (reversed_minor; `reversed` once all of it is gone); after it is paid it is a negative row of its own,
-- approved at once, that the next payout nets. Every reversal is recorded once under its key, so a repeated credit-note event takes back nothing twice.
CREATE TABLE affiliate_commissions (
  tenant_id           uuid NOT NULL,
  id                  uuid NOT NULL DEFAULT gen_random_uuid(),
  affiliate_id        uuid NOT NULL,
  customer_id         uuid NOT NULL,
  kind                text NOT NULL DEFAULT 'commission' CHECK (kind IN ('commission', 'reversal')),
  invoice_id          uuid NOT NULL,
  invoice_number      text NOT NULL DEFAULT '',
  currency            char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  -- What the invoice came to, tax included, and the base the commission was taken from: the net, pre-tax amount actually paid.
  invoice_total_minor bigint NOT NULL CHECK (invoice_total_minor >= 0),
  net_minor           bigint NOT NULL CHECK (net_minor >= 0),
  -- The commission, in the invoice's currency; negative on a reversal row.
  amount_minor        bigint NOT NULL CHECK (amount_minor <> 0),
  -- How much of a commission has been taken back so far (a reversal row records its own amount instead).
  reversed_minor      bigint NOT NULL DEFAULT 0 CHECK (reversed_minor >= 0),
  status              text NOT NULL CHECK (status IN ('pending', 'approved', 'paid', 'reversed')),
  parent_id           uuid,
  reversal_key        text,
  earned_at           timestamptz NOT NULL,
  approve_after       timestamptz NOT NULL,
  approved_at         timestamptz,
  paid_at             timestamptz,
  payout_id           uuid,
  created_at          timestamptz NOT NULL DEFAULT now(),
  -- What a commission was actually paid out for: its amount less what had been taken back by then (a negative row: its own amount).
  settled_minor       bigint,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
  FOREIGN KEY (tenant_id, parent_id) REFERENCES affiliate_commissions (tenant_id, id),
  CONSTRAINT affiliate_commissions_payout_fkey FOREIGN KEY (tenant_id, payout_id) REFERENCES affiliate_payouts (tenant_id, id),
  CONSTRAINT commission_shape CHECK (
    (kind = 'commission' AND amount_minor > 0 AND parent_id IS NULL AND reversal_key IS NULL AND reversed_minor <= amount_minor)
    OR (kind = 'reversal' AND amount_minor < 0 AND parent_id IS NOT NULL AND reversal_key IS NOT NULL AND reversed_minor = 0 AND status IN ('approved', 'paid'))
  ),
  CONSTRAINT commission_status_matches CHECK (
    (status = 'paid') = (paid_at IS NOT NULL) AND (status <> 'approved' OR approved_at IS NOT NULL)
  ),
  CONSTRAINT commission_settled_when_paid CHECK ((status = 'paid') = (settled_minor IS NOT NULL))
);
-- Replaying invoice.paid, or two instances handling it at once, makes one commission.
CREATE UNIQUE INDEX affiliate_commissions_invoice ON affiliate_commissions (tenant_id, invoice_id) WHERE kind = 'commission';
CREATE UNIQUE INDEX affiliate_commissions_reversal_key ON affiliate_commissions (tenant_id, reversal_key) WHERE reversal_key IS NOT NULL;
CREATE INDEX affiliate_commissions_by_affiliate ON affiliate_commissions (tenant_id, affiliate_id, status, earned_at DESC, id);
CREATE INDEX affiliate_commissions_by_customer ON affiliate_commissions (tenant_id, customer_id, earned_at);
CREATE INDEX affiliate_commissions_by_date ON affiliate_commissions (tenant_id, earned_at DESC, id);
-- The job that approves commissions finds them by this alone, across tenants.
CREATE INDEX affiliate_commissions_due ON affiliate_commissions (approve_after) WHERE status = 'pending';
CREATE INDEX affiliate_commissions_by_payout ON affiliate_commissions (tenant_id, payout_id) WHERE payout_id IS NOT NULL;
-- What the owner can pay: approved and not in a payout yet.
CREATE INDEX affiliate_commissions_payable ON affiliate_commissions (tenant_id, affiliate_id, currency) WHERE status = 'approved' AND payout_id IS NULL;

CREATE TABLE affiliate_reversals (
  tenant_id       uuid NOT NULL,
  key             text NOT NULL CHECK (length(key) BETWEEN 1 AND 300),
  commission_id   uuid NOT NULL,
  invoice_id      uuid NOT NULL,
  kind            text NOT NULL CHECK (kind IN ('void', 'credit_note', 'chargeback', 'manual')),
  -- For a credit note, what it credited of the invoice (gross); the sum of these is how much of the invoice has been credited back.
  credited_minor  bigint NOT NULL DEFAULT 0 CHECK (credited_minor >= 0),
  -- How much of the commission this event took back.
  amount_minor    bigint NOT NULL CHECK (amount_minor >= 0),
  note            text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  created_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, key),
  FOREIGN KEY (tenant_id, commission_id) REFERENCES affiliate_commissions (tenant_id, id)
);
CREATE INDEX affiliate_reversals_by_commission ON affiliate_reversals (tenant_id, commission_id);

CREATE TABLE affiliate_journal (
  tenant_id    uuid NOT NULL,
  id           uuid NOT NULL DEFAULT gen_random_uuid(),
  payout_id    uuid NOT NULL,
  affiliate_id uuid NOT NULL,
  posted_at    timestamptz NOT NULL,
  currency     char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  account      text NOT NULL CHECK (account IN ('commission_expense', 'commissions_payable', 'cash')),
  debit_minor  bigint NOT NULL DEFAULT 0 CHECK (debit_minor >= 0),
  credit_minor bigint NOT NULL DEFAULT 0 CHECK (credit_minor >= 0),
  memo         text NOT NULL DEFAULT '',
  sandbox      boolean NOT NULL,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, payout_id) REFERENCES affiliate_payouts (tenant_id, id),
  FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates (tenant_id, id),
  CONSTRAINT one_side CHECK ((debit_minor > 0) <> (credit_minor > 0))
);
CREATE INDEX affiliate_journal_by_payout ON affiliate_journal (tenant_id, payout_id);
CREATE INDEX affiliate_journal_by_date ON affiliate_journal (tenant_id, posted_at DESC, id);

-- The books balance per payout and currency, checked when the transaction commits, so a set of lines may be written in any order.
-- +goose StatementBegin
CREATE FUNCTION affiliate_journal_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
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
CREATE CONSTRAINT TRIGGER affiliate_journal_balanced AFTER INSERT ON affiliate_journal DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION affiliate_journal_balanced();

-- The journal is append-only: no UPDATE, no DELETE for the application role. The DELETE is allowed for a sandbox tenant (a reset) and for a tenant that has a cell
-- (the last step of moving it out of the pooled database; see affiliate_attributions_immutable above).
-- +goose StatementBegin
CREATE FUNCTION affiliate_journal_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND EXISTS (SELECT 1 FROM tenants WHERE id = OLD.tenant_id AND (environment = 'sandbox' OR cell <> '')) THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'the affiliate journal is append-only' USING ERRCODE = 'P0001', HINT = 'journal_append_only';
END $$;
-- +goose StatementEnd
CREATE TRIGGER affiliate_journal_append_only BEFORE UPDATE OR DELETE ON affiliate_journal FOR EACH ROW EXECUTE FUNCTION affiliate_journal_append_only();

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Outbound webhooks
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- Outbound events. A tenant registers HTTPS endpoints; the platform records an event for what happens to its subscriptions, invoices and credit notes
-- and delivers it, signed, to every active endpoint that wants that type, retrying with a backoff. An event has one id however often it is delivered.
CREATE TABLE webhook_endpoints (
  tenant_id         uuid NOT NULL,
  id                uuid NOT NULL,
  url               text NOT NULL CHECK (length(url) <= 2000),
  description       text NOT NULL DEFAULT '' CHECK (length(description) <= 200),
  event_types       text[] NOT NULL DEFAULT '{}',          -- empty: every type
  secret_enc        bytea NOT NULL,                          -- sealed with QUOTIFY_SECRETS_KEY
  prev_secret_enc   bytea,                                   -- still signed with, until prev_secret_until
  prev_secret_until timestamptz,
  active            boolean NOT NULL DEFAULT true,
  created_at        timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id)
);

CREATE TABLE webhook_events (
  tenant_id   uuid NOT NULL,
  id          uuid NOT NULL,
  type        text NOT NULL,
  data        jsonb NOT NULL,
  occurred_at timestamptz NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id)
);

CREATE TABLE webhook_deliveries (
  tenant_id       uuid NOT NULL,
  id              uuid NOT NULL,
  event_id        uuid NOT NULL,
  endpoint_id     uuid NOT NULL,
  state           text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'delivered', 'failed')),
  attempts        int NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),      -- while pending: when the dispatcher may try again (also its lease)
  last_status     int,
  last_error      text,
  last_attempt_at timestamptz,
  delivered_at    timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, event_id, endpoint_id),
  FOREIGN KEY (tenant_id, event_id) REFERENCES webhook_events (tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, endpoint_id) REFERENCES webhook_endpoints (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX webhook_deliveries_due ON webhook_deliveries (next_attempt_at) WHERE state = 'pending';
CREATE INDEX webhook_deliveries_by_endpoint ON webhook_deliveries (tenant_id, endpoint_id, created_at DESC);

-- Live webhook endpoints follow the same rule as live API keys. The trigger reads the tenant through current_tenant() because the app role cannot read the table.
-- +goose StatementBegin
CREATE FUNCTION webhook_endpoints_check_tenant() RETURNS trigger
LANGUAGE plpgsql AS $$
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
CREATE TRIGGER webhook_endpoints_tenant_check BEFORE INSERT OR UPDATE OF active ON webhook_endpoints
  FOR EACH ROW EXECUTE FUNCTION webhook_endpoints_check_tenant();

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Row level security on every tenant-owned table the app role can use. FORCE applies it to the owner too (plans_system has BYPASSRLS).
-- No row level security on purpose on: outbox, api_keys (found by prefix, before there is a tenant) and sandbox_resets (the system role names the tenant itself; listed
-- in the RLS allowlist of the integration test), and on the tables that have no tenant_id.
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

ALTER TABLE customers ENABLE ROW LEVEL SECURITY;
ALTER TABLE customers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customers USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE meters ENABLE ROW LEVEL SECURITY;
ALTER TABLE meters FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON meters USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE plans ENABLE ROW LEVEL SECURITY;
ALTER TABLE plans FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON plans USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE plan_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE plan_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON plan_versions USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE plan_version_fees ENABLE ROW LEVEL SECURITY;
ALTER TABLE plan_version_fees FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON plan_version_fees USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE plan_prices ENABLE ROW LEVEL SECURITY;
ALTER TABLE plan_prices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON plan_prices USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE plan_currency_prices ENABLE ROW LEVEL SECURITY;
ALTER TABLE plan_currency_prices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON plan_currency_prices USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE plan_entitlements ENABLE ROW LEVEL SECURITY;
ALTER TABLE plan_entitlements FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON plan_entitlements USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE customer_entitlement_overrides ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_entitlement_overrides FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_entitlement_overrides USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE subscriptions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON subscriptions USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE subscription_changes ENABLE ROW LEVEL SECURITY;
ALTER TABLE subscription_changes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON subscription_changes USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE subscription_adjustments ENABLE ROW LEVEL SECURITY;
ALTER TABLE subscription_adjustments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON subscription_adjustments USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE provider_migrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE provider_migrations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON provider_migrations USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE subscription_trial_notices ENABLE ROW LEVEL SECURITY;
ALTER TABLE subscription_trial_notices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON subscription_trial_notices USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE routing_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE routing_rules FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON routing_rules USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE tax_rates ENABLE ROW LEVEL SECURITY;
ALTER TABLE tax_rates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tax_rates USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE tenant_mail_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_mail_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_mail_settings USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE tenant_mail_integrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_mail_integrations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_mail_integrations USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE coupons ENABLE ROW LEVEL SECURITY;
ALTER TABLE coupons FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON coupons USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE promotion_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE promotion_codes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON promotion_codes USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE coupon_redemptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE coupon_redemptions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON coupon_redemptions USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE customer_coupons ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_coupons FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_coupons USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE subscription_coupons ENABLE ROW LEVEL SECURITY;
ALTER TABLE subscription_coupons FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON subscription_coupons USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE affiliate_programs ENABLE ROW LEVEL SECURITY;
ALTER TABLE affiliate_programs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON affiliate_programs USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE affiliates ENABLE ROW LEVEL SECURITY;
ALTER TABLE affiliates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON affiliates USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE affiliate_clicks_daily ENABLE ROW LEVEL SECURITY;
ALTER TABLE affiliate_clicks_daily FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON affiliate_clicks_daily USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE affiliate_attributions ENABLE ROW LEVEL SECURITY;
ALTER TABLE affiliate_attributions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON affiliate_attributions USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE affiliate_attribution_rejections ENABLE ROW LEVEL SECURITY;
ALTER TABLE affiliate_attribution_rejections FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON affiliate_attribution_rejections USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE affiliate_payouts ENABLE ROW LEVEL SECURITY;
ALTER TABLE affiliate_payouts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON affiliate_payouts USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE affiliate_commissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE affiliate_commissions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON affiliate_commissions USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE affiliate_reversals ENABLE ROW LEVEL SECURITY;
ALTER TABLE affiliate_reversals FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON affiliate_reversals USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE affiliate_journal ENABLE ROW LEVEL SECURITY;
ALTER TABLE affiliate_journal FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON affiliate_journal USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE webhook_endpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_endpoints FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON webhook_endpoints USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE webhook_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON webhook_events USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE webhook_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_deliveries FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON webhook_deliveries USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Grants
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- plans_app: append-only tables (meters, plans, plan versions and their prices/entitlements, the affiliate attributions and journal) get no UPDATE/DELETE.
-- The identity tables, the apps, the tenants and the keys are not granted to it at all.
GRANT SELECT, INSERT, UPDATE ON customers, subscriptions, provider_migrations, subscription_changes, subscription_adjustments, coupons, promotion_codes, coupon_redemptions,
  customer_coupons, tenant_mail_settings, affiliate_programs, affiliates, affiliate_clicks_daily, affiliate_commissions, affiliate_payouts TO plans_app;
GRANT UPDATE (address, tax_id, tax_office, e_invoice_registered) ON customers TO plans_app;
GRANT SELECT, INSERT ON meters, plans, plan_versions, plan_version_fees, plan_prices, plan_currency_prices, plan_entitlements, webhook_events, subscription_trial_notices,
  affiliate_attributions, affiliate_attribution_rejections, affiliate_reversals, affiliate_journal TO plans_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON customer_entitlement_overrides, subscription_coupons, tenant_mail_integrations, webhook_endpoints, webhook_deliveries TO plans_app;
GRANT SELECT, INSERT, DELETE ON routing_rules, tax_rates TO plans_app;
GRANT INSERT ON outbox TO plans_app;
-- The app role reads its own tenant row through current_tenant() only (tenants itself is operator-only).
GRANT EXECUTE ON FUNCTION current_tenant(), app_tenant_id() TO plans_app;

-- plans_system: the operator, the identity module, the public affiliate link and the jobs (the outbox relay, the webhook dispatcher, the affiliate commission
-- approver, the trial reminder, the sandbox reset runner and the retention sweep).
GRANT SELECT, INSERT, UPDATE ON accounts, tenants, api_keys, users, user_identities, user_onboarding, account_invites, sandbox_resets, affiliates TO plans_system;
GRANT SELECT, INSERT ON apps, app_audit, config_promotions, go_live_audit TO plans_system;
-- Name and slug change on rename and archive; nothing else about an app does.
GRANT UPDATE (name, slug, archived_at, archived_by) ON apps TO plans_system;
-- Members change role and leave or are removed (account_members).
GRANT SELECT, INSERT, UPDATE, DELETE ON account_members, user_sessions, user_tokens, login_codes, affiliate_clicks_daily TO plans_system;
-- FOR UPDATE SKIP LOCKED needs an UPDATE privilege on at least one column; id is GENERATED ALWAYS, so it is never actually changed.
GRANT SELECT, DELETE ON outbox TO plans_system;
GRANT UPDATE (id) ON outbox TO plans_system;
-- The system role serves the public affiliate link (it has no tenant), the partner's own portal (a person, not a tenant), the dispatcher (across tenants) and
-- the reads of the other services.
GRANT SELECT ON meters, plans, plan_versions, plan_version_fees, plan_prices, plan_currency_prices, plan_entitlements, coupons, promotion_codes, routing_rules, tax_rates,
  tenant_mail_settings, tenant_mail_integrations, webhook_endpoints, affiliate_programs TO plans_system;
-- A reset of a sandbox deletes what describes the customers (customers, subscriptions and their history, webhook history, the affiliate program's customer side, the
-- sandbox's quota overrides and trial notices), nothing of the configuration.
GRANT SELECT, DELETE ON customers, subscriptions, provider_migrations, subscription_changes, subscription_adjustments, subscription_coupons, customer_coupons,
  coupon_redemptions, customer_entitlement_overrides, subscription_trial_notices, webhook_events, affiliate_attributions, affiliate_attribution_rejections,
  affiliate_reversals, affiliate_payouts, affiliate_journal TO plans_system;
GRANT SELECT, UPDATE, DELETE ON affiliate_commissions, webhook_deliveries TO plans_system;
-- A coupon's count of redemptions starts at zero again with the ledger it counts.
GRANT UPDATE (redeemed) ON coupons, promotion_codes TO plans_system;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO plans_system;

-- +goose Down
-- current_tenant() returns the row type of tenants, so it goes first.
DROP FUNCTION current_tenant();
-- The extension btree_gist is left in place: it may have been installed by whoever made the database.
DROP TABLE webhook_deliveries, webhook_events, webhook_endpoints, affiliate_journal, affiliate_reversals, affiliate_commissions, affiliate_payouts,
  affiliate_attribution_rejections, affiliate_attributions, affiliate_clicks_daily, subscription_coupons, customer_coupons, coupon_redemptions, promotion_codes, affiliates,
  affiliate_programs, coupons, outbox, tenant_mail_integrations, tenant_mail_settings, tax_rates, routing_rules, subscription_trial_notices, provider_migrations,
  subscription_adjustments, subscription_changes, subscriptions, customer_entitlement_overrides, plan_entitlements, plan_currency_prices, plan_prices, plan_version_fees,
  plan_versions, plans, meters, customers, sandbox_resets, go_live_audit, config_promotions, app_audit, api_keys, tenants, user_onboarding, user_identities, login_codes,
  account_invites, user_tokens, user_sessions, account_members, users, apps, accounts;
DROP FUNCTION webhook_endpoints_check_tenant(), affiliate_journal_append_only(), affiliate_journal_balanced(), affiliate_attributions_immutable(),
  sandbox_resets_only_sandbox(), go_live_audit_live_only(), config_promotions_one_way(), api_keys_check_app_not_archived(), api_keys_check_tenant(),
  tenants_immutable_settings(), app_tenant_id();
