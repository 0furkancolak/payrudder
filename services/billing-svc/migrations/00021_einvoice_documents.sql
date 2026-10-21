-- Phase 30: the legal e-document of an invoice or a credit note and where it stands (docs/phases/phase-30-e-invoice.md).
-- id is uuidv5(tenant|kind|source id): it is also the UBL UUID at the integrator, so one source can never have two documents.
-- request is the frozen IssueEInvoice command; a retry sends the same document (only the parties are refreshed by the operator's retry).
-- +goose Up
CREATE TABLE einvoice_documents (
  tenant_id         uuid NOT NULL,
  id                uuid NOT NULL,
  kind              text NOT NULL CHECK (kind IN ('invoice', 'credit_note')),
  invoice_id        uuid NOT NULL,
  credit_note_id    uuid,
  number            text NOT NULL,
  environment       text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  status            text NOT NULL CHECK (status IN ('pending', 'issued', 'failed', 'cancelled')),
  document_type     text NOT NULL DEFAULT '',
  provider          text NOT NULL DEFAULT '',
  -- attempts only grows (it is part of the command id); streak counts the automatic attempts since the last operator action and drives the backoff.
  attempts          int NOT NULL DEFAULT 0,
  streak            int NOT NULL DEFAULT 0,
  -- pending: when the next attempt is due (NULL while a command is out; then last_dispatch_at + the reply wait applies)
  next_attempt_at   timestamptz,
  last_dispatch_at  timestamptz,
  cancel_requested  boolean NOT NULL DEFAULT false,
  last_error_key    text NOT NULL DEFAULT '',
  last_error        text NOT NULL DEFAULT '',
  legal_number      text,
  external_id       text NOT NULL DEFAULT '',
  xml_key           text,
  pdf_key           text,
  request           bytea NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  issued_at         timestamptz,
  updated_at        timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  CHECK ((kind = 'credit_note') = (credit_note_id IS NOT NULL)),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);
CREATE UNIQUE INDEX one_einvoice_per_invoice ON einvoice_documents (tenant_id, invoice_id) WHERE kind = 'invoice';
CREATE UNIQUE INDEX one_einvoice_per_credit_note ON einvoice_documents (tenant_id, credit_note_id) WHERE kind = 'credit_note';
-- A legal number is never reused: one document per number within a tenant and environment.
CREATE UNIQUE INDEX einvoice_legal_number_once ON einvoice_documents (tenant_id, environment, legal_number) WHERE legal_number IS NOT NULL;
CREATE INDEX einvoice_due ON einvoice_documents (status, next_attempt_at, last_dispatch_at) WHERE status = 'pending' OR cancel_requested;
CREATE INDEX einvoice_by_tenant ON einvoice_documents (tenant_id, created_at DESC, id DESC);

-- +goose StatementBegin
DO $$
BEGIN
  ALTER TABLE einvoice_documents ENABLE ROW LEVEL SECURITY;
  ALTER TABLE einvoice_documents FORCE ROW LEVEL SECURITY;
  CREATE POLICY tenant_isolation ON einvoice_documents USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
END $$;
-- +goose StatementEnd
GRANT SELECT ON einvoice_documents TO billing_app;
GRANT SELECT, INSERT, UPDATE ON einvoice_documents TO billing_system;

-- +goose Down
DROP TABLE einvoice_documents;
