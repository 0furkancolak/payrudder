-- Phase 29: bank transfers are confirmed by hand. A confirmed receipt is a ledger row with source 'manual' (kind charge, status succeeded) plus a row here that
-- keeps what the ledger has no columns for: the bank's reference, when the money arrived, the note and who recorded it.
-- +goose Up
ALTER TABLE payment_ledger DROP CONSTRAINT payment_ledger_source_check;
ALTER TABLE payment_ledger ADD CONSTRAINT payment_ledger_source_check CHECK (source IN ('command', 'webhook', 'reconciliation', 'manual'));

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

GRANT SELECT, INSERT ON manual_receipts TO payments_system;
GRANT SELECT ON manual_receipts TO payments_app;

-- +goose Down
DROP TABLE manual_receipts;
DELETE FROM payment_ledger WHERE source = 'manual';
ALTER TABLE payment_ledger DROP CONSTRAINT payment_ledger_source_check;
ALTER TABLE payment_ledger ADD CONSTRAINT payment_ledger_source_check CHECK (source IN ('command', 'webhook', 'reconciliation'));
