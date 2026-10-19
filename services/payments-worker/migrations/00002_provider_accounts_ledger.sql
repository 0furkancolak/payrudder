-- Phase 14: a tenant's own provider accounts, an append-only ledger of everything that happened at a provider, and the inbox of
-- provider notifications (docs/14-billing-platform.md).
-- +goose Up

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

-- Every charge, refund, dispute and payout the provider told us about, or that we asked it for. Append-only: a correction is a new row.
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
  source           text NOT NULL CHECK (source IN ('command', 'webhook', 'reconciliation')),
  detail           text,
  occurred_at      timestamptz NOT NULL DEFAULT now()
);
-- The same fact reported twice (the command's answer, then the provider's notification) is one row.
CREATE UNIQUE INDEX ledger_fact_once ON payment_ledger (provider, kind, provider_ref, status) WHERE provider_ref IS NOT NULL;
CREATE INDEX ledger_by_invoice ON payment_ledger (tenant_id, invoice_id);
CREATE INDEX ledger_report ON payment_ledger (tenant_id, occurred_at);

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

-- A charge is re-sent under the same idempotency key after "not found", so the key is not unique; the webhook finds its attempt by it.
CREATE INDEX payment_attempts_charge_key ON payment_attempts (idempotency_key) WHERE kind = 'charge';

GRANT SELECT, INSERT, UPDATE, DELETE ON provider_accounts TO payments_system;
GRANT SELECT, INSERT ON payment_ledger TO payments_system;
GRANT SELECT, INSERT, UPDATE, DELETE ON provider_events TO payments_system;
GRANT SELECT ON payment_ledger, provider_events TO payments_app;

-- +goose Down
DROP INDEX payment_attempts_charge_key;
DROP TABLE provider_events, payment_ledger, provider_accounts;
