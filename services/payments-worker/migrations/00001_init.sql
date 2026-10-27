-- payments_db (docs/05-data-model.md#payments_db): payments-worker's own state. It holds no money logic and no tenant
-- data beyond ids, so it has no RLS: only payments_system touches it (payments_app only reads).
-- This is the baseline: it was written once, before the first release, from the schema the pre-release migrations had built, and it starts the history.
-- Every change from here on is a new numbered migration (00002_...), never an edit of this file.

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
-- A charge is re-sent under the same idempotency key after "not found", so the key is not unique; the webhook finds its attempt by it.
CREATE INDEX payment_attempts_charge_key ON payment_attempts (idempotency_key) WHERE kind = 'charge';

-- Credentials of a tenant at a provider, sealed with AES-256-GCM (pkg/secretbox). The context bound into the box is
-- tenant|provider|environment, so a copy of the column in another row does not open.
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
-- At most one default account per tenant and environment: the provider live charges go to unless the command names one.
CREATE UNIQUE INDEX one_default_provider_account ON provider_accounts (tenant_id, environment) WHERE is_default;

-- Who connected, replaced or removed a tenant's provider account, and whether the provider was asked to vouch for the credentials. Append-only:
-- a correction is a new row. No secret is ever written here; the settings stay sealed in provider_accounts.
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

-- Every charge, refund, dispute and payout the provider told us about, or that we asked it for. Append-only: a correction is a new row.
-- The ledger is append-only for the product (no role of the request path can delete from it); only payments_system, and only the sandbox reset, does.
-- source 'manual': bank transfers are confirmed by hand; a confirmed receipt is a ledger row (kind charge, status succeeded) plus a row in manual_receipts.
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
  source           text NOT NULL CHECK (source IN ('command', 'webhook', 'reconciliation', 'manual')),
  detail           text,
  occurred_at      timestamptz NOT NULL DEFAULT now()
);
-- The same fact reported twice (the command's answer, then the provider's notification) is one row. It must not span tenants: the bank-transfer reference
-- defaults to the invoice number, and two tenants can both have INV-2026-000001; without tenant_id the second tenant's pending row was dropped silently
-- by ON CONFLICT DO NOTHING.
CREATE UNIQUE INDEX ledger_fact_once ON payment_ledger (tenant_id, provider, kind, provider_ref, status) WHERE provider_ref IS NOT NULL;
CREATE INDEX ledger_by_invoice ON payment_ledger (tenant_id, invoice_id);
CREATE INDEX ledger_report ON payment_ledger (tenant_id, occurred_at);

-- Bank transfers are confirmed by hand: a row here keeps what the ledger has no columns for: the bank's reference, when the money arrived, the note and who
-- recorded it.
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
  -- The same bank reference for the same invoice is the same transfer: confirming it again changes nothing.
  UNIQUE (tenant_id, invoice_id, bank_reference)
);

-- Notifications from a provider, deduplicated on the provider's own event id. The body is kept as received.
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

-- Subscriptions a provider runs for us (Paddle), as the provider's notifications report them.
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

-- What the platform has created at a provider (products, prices, customers, discounts), so it is created once and found again.
CREATE TABLE provider_objects (
  tenant_id   uuid NOT NULL,
  provider    text NOT NULL,
  environment text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  kind        text NOT NULL CHECK (kind IN ('product', 'price', 'customer', 'address', 'business', 'discount')),
  key         text NOT NULL,   -- the platform's name for it: plan:{code}, {plan version id}|{interval}|{currency}, cus:{customer id}, ...
  ref         text NOT NULL,   -- the provider's id
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, provider, environment, kind, key)
);
CREATE INDEX provider_objects_by_ref ON provider_objects (tenant_id, provider, environment, kind, ref);

-- E-invoicing (docs/phases/phase-30-e-invoice.md). Like provider_accounts, the two tables below hold ids and sealed credentials only (the tenant-facing state of a
-- document is billing-svc's einvoice_documents, with RLS), so only payments_system touches them.

-- A tenant's e-invoice integrator account, one per environment. The settings (API key, URL, secret) are sealed with AES-256-GCM (pkg/secretbox); the box
-- is bound to tenant|einvoice|environment, so a copy of the column in another row does not open.
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

-- One row per document UUID: the idempotency record of the issue. A row is written before the integrator is called, so a crash between the integrator's
-- answer and ours is repaired by asking the integrator again under the same UUID. Once issued, the row is final: it is never updated again except to
-- cancelled, and a legal number is unique within a tenant and environment, so it can never be handed to a second document.
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

GRANT SELECT, INSERT, UPDATE ON payment_attempts TO payments_system;
GRANT SELECT, INSERT, UPDATE, DELETE ON provider_accounts TO payments_system;
GRANT SELECT, INSERT ON provider_account_events TO payments_system;
GRANT SELECT, INSERT ON payment_ledger TO payments_system;
GRANT SELECT, INSERT ON manual_receipts TO payments_system;
GRANT SELECT, INSERT, UPDATE, DELETE ON provider_events TO payments_system;
GRANT SELECT, INSERT, UPDATE ON provider_subscriptions TO payments_system;
GRANT SELECT, INSERT ON provider_objects TO payments_system;
GRANT SELECT, INSERT, UPDATE, DELETE ON einvoice_accounts TO payments_system;
GRANT SELECT, INSERT, UPDATE ON einvoice_issuances TO payments_system;
-- Resetting a sandbox deletes a tenant's attempts, ledger rows and what hangs on them.
GRANT DELETE ON manual_receipts, payment_ledger, payment_attempts, provider_subscriptions, einvoice_issuances TO payments_system;

GRANT SELECT ON payment_attempts, provider_account_events, payment_ledger, manual_receipts, provider_events, provider_subscriptions, provider_objects TO payments_app;

-- +goose Down
DROP TABLE einvoice_issuances, einvoice_accounts, provider_objects, provider_subscriptions, provider_events, manual_receipts, payment_ledger, provider_account_events,
  provider_accounts, payment_attempts;
