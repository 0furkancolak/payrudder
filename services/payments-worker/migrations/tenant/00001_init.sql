-- payments_db, tenant schema (phase 28, ADR 0018): what one tenant of a cell owns, in its own schema t_<id>.
--
-- It is ../00001 to ../00009 as they end up, written once for a schema: nothing in it names a schema, so the same statements would build the tables and grants in
-- public. They run with the search path set to the tenant's schema; each schema keeps its own goose_db_version. A change to ../ is a change here too:
-- TestTenantSchemaMatchesPublic compares the two. Roles are the service's own (payments_app, payments_system).
--
-- Like the public tables, none of these has row-level security: only payments_system writes them, and every statement names the tenant. In a cell the schema is
-- the boundary: a transaction of the service runs with the search path of one tenant's schema (pgxx.Router), and that alone decides whose rows it can see.

-- +goose Up
CREATE TABLE payment_attempts (
  command_id       uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL,
  invoice_id       uuid NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('charge', 'refund', 'status')),
  environment      text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  provider         text NOT NULL,
  idempotency_key  text NOT NULL,
  request          jsonb NOT NULL,
  reply            bytea,                       -- the stored ReplyEnvelope; NULL while STARTED
  status           text NOT NULL CHECK (status IN ('STARTED', 'DONE')),
  created_at       timestamptz NOT NULL DEFAULT now(),
  finished_at      timestamptz
);
CREATE INDEX payment_attempts_invoice ON payment_attempts (invoice_id, kind);
CREATE INDEX payment_attempts_charge_key ON payment_attempts (idempotency_key) WHERE kind = 'charge';

CREATE TABLE provider_accounts (
  tenant_id     uuid NOT NULL,
  provider      text NOT NULL,
  environment   text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  is_default    boolean NOT NULL DEFAULT false,
  settings_enc  bytea NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, provider, environment)
);
CREATE UNIQUE INDEX one_default_provider_account ON provider_accounts (tenant_id, environment) WHERE is_default;

CREATE TABLE payment_ledger (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id        uuid NOT NULL,
  invoice_id       uuid,
  provider         text NOT NULL,
  environment      text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  kind             text NOT NULL CHECK (kind IN ('charge', 'refund', 'dispute', 'payout')),
  status           text NOT NULL CHECK (status IN ('succeeded', 'declined', 'failed', 'pending')),
  amount_minor     bigint NOT NULL CHECK (amount_minor >= 0),
  currency         char(3) NOT NULL,
  provider_ref     text,
  idempotency_key  text,
  source           text NOT NULL,
  detail           text,
  occurred_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT payment_ledger_source_check CHECK (source IN ('command', 'webhook', 'reconciliation', 'manual'))
);
CREATE UNIQUE INDEX ledger_fact_once ON payment_ledger (tenant_id, provider, kind, provider_ref, status) WHERE provider_ref IS NOT NULL;
CREATE INDEX ledger_by_invoice ON payment_ledger (tenant_id, invoice_id);
CREATE INDEX ledger_report ON payment_ledger (tenant_id, occurred_at);

CREATE TABLE provider_events (
  provider      text NOT NULL,
  event_id      text NOT NULL,
  tenant_id     uuid,
  received_at   timestamptz NOT NULL DEFAULT now(),
  body          jsonb NOT NULL,
  status        text NOT NULL CHECK (status IN ('received', 'processed', 'ignored', 'failed')),
  processed_at  timestamptz,
  error         text,
  PRIMARY KEY (provider, event_id)
);

CREATE TABLE provider_subscriptions (
  tenant_id              uuid NOT NULL,
  provider               text NOT NULL,
  environment            text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  provider_ref           text NOT NULL,
  quotify_subscription_id uuid,                 -- the subscription this one was created for, when the provider says
  status                 text NOT NULL,         -- the provider's own word: active, trialing, past_due, paused, canceled
  next_billed_at         timestamptz,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, provider, environment, provider_ref)
);
CREATE INDEX provider_subscriptions_quotify ON provider_subscriptions (tenant_id, quotify_subscription_id) WHERE quotify_subscription_id IS NOT NULL;

CREATE TABLE provider_objects (
  tenant_id   uuid NOT NULL,
  provider    text NOT NULL,
  environment text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  kind        text NOT NULL CHECK (kind IN ('product', 'price', 'customer', 'address', 'business', 'discount')),
  key         text NOT NULL,   -- Quotify's name for it: plan:{code}, {plan version id}|{interval}|{currency}, cus:{customer id}, ...
  ref         text NOT NULL,   -- the provider's id
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, provider, environment, kind, key)
);
CREATE INDEX provider_objects_by_ref ON provider_objects (tenant_id, provider, environment, kind, ref);

CREATE TABLE manual_receipts (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id       uuid NOT NULL,
  invoice_id      uuid NOT NULL,
  ledger_id       bigint NOT NULL REFERENCES payment_ledger (id),
  bank_reference  text NOT NULL,
  amount_minor    bigint NOT NULL CHECK (amount_minor > 0),
  currency        char(3) NOT NULL,
  received_at     timestamptz NOT NULL,
  note            text,
  recorded_by     text NOT NULL,
  recorded_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, invoice_id, bank_reference)
);

CREATE TABLE einvoice_accounts (
  tenant_id     uuid NOT NULL,
  environment   text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  provider      text NOT NULL,
  mode          text NOT NULL CHECK (mode IN ('test', 'production')),
  settings_enc  bytea NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, environment)
);

CREATE TABLE einvoice_issuances (
  document_id    uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL,
  environment    text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  provider       text NOT NULL,
  kind           text NOT NULL CHECK (kind IN ('invoice', 'credit_note')),
  document_type  text NOT NULL,
  status         text NOT NULL CHECK (status IN ('started', 'issued', 'cancelled')),
  legal_number   text,
  external_id    text NOT NULL DEFAULT '',
  xml_key        text,
  pdf_key        text,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CHECK (status = 'started' OR legal_number IS NOT NULL)
);
CREATE UNIQUE INDEX einvoice_legal_number_once ON einvoice_issuances (tenant_id, environment, legal_number) WHERE legal_number IS NOT NULL;

CREATE TABLE provider_account_events (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id        uuid NOT NULL,
  provider         text NOT NULL,
  environment      text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  action           text NOT NULL CHECK (action IN ('connected', 'replaced', 'removed')),
  actor            text NOT NULL DEFAULT '',        -- "user:<uuid>" for a person in the console, "operator" for support, '' when nobody was named
  is_default       boolean NOT NULL DEFAULT false,
  verified_remote  boolean NOT NULL DEFAULT false,  -- the provider itself accepted the credentials when they were stored
  created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX provider_account_events_by_tenant ON provider_account_events (tenant_id, created_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON payment_attempts, provider_accounts, provider_events, provider_subscriptions, einvoice_accounts, einvoice_issuances TO payments_system;
GRANT SELECT, INSERT, DELETE ON payment_ledger, manual_receipts TO payments_system;
GRANT SELECT, INSERT ON provider_objects, provider_account_events TO payments_system;

GRANT SELECT ON payment_attempts, payment_ledger, provider_events, provider_subscriptions, provider_objects, manual_receipts, provider_account_events TO payments_app;

-- +goose Down
DROP TABLE provider_account_events, einvoice_issuances, einvoice_accounts, manual_receipts, provider_objects, provider_subscriptions, provider_events, payment_ledger,
  provider_accounts, payment_attempts;
