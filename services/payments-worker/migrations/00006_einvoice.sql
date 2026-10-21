-- Phase 30: e-invoicing (docs/phases/phase-30-e-invoice.md). Like provider_accounts, these hold ids and sealed credentials only (the tenant-facing state of a
-- document is billing-svc's einvoice_documents, with RLS), so only payments_system touches them.
-- +goose Up

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

GRANT SELECT, INSERT, UPDATE, DELETE ON einvoice_accounts TO payments_system;
GRANT SELECT, INSERT, UPDATE ON einvoice_issuances TO payments_system;

-- +goose Down
DROP TABLE einvoice_issuances, einvoice_accounts;
