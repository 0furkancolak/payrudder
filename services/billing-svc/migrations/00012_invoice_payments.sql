-- Phase 29: money received outside a payment provider. An operator records it against an invoice (cash, a bank transfer, other) with who and when; the invoice is
-- paid when the payments add up to the total. Append-only: a correction is another row.
-- +goose Up
CREATE TABLE invoice_payments (
  tenant_id    uuid NOT NULL,
  id           uuid NOT NULL,
  invoice_id   uuid NOT NULL,
  method       text NOT NULL CHECK (method IN ('bank_transfer', 'cash', 'other')),
  amount_minor bigint NOT NULL CHECK (amount_minor > 0),
  currency     char(3) NOT NULL,
  received_at  timestamptz NOT NULL,
  reference    text NOT NULL DEFAULT '',   -- the bank's or the receipt's reference; with the method it makes the payment idempotent
  note         text NOT NULL DEFAULT '',
  recorded_by  text NOT NULL,
  recorded_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);
CREATE UNIQUE INDEX invoice_payment_once ON invoice_payments (tenant_id, invoice_id, method, reference) WHERE reference <> '';
CREATE INDEX invoice_payments_by_invoice ON invoice_payments (tenant_id, invoice_id, received_at);
ALTER TABLE invoice_payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_payments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoice_payments USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT ON invoice_payments TO billing_app;
GRANT SELECT, INSERT ON invoice_payments TO billing_system;

-- Sagas that wait for a payment to settle (a bank transfer) are polled when next_attempt_at is due.
CREATE INDEX saga_polls ON invoice_sagas (next_attempt_at) WHERE state = 'PAYMENT_PENDING';

-- +goose Down
DROP INDEX saga_polls;
DROP TABLE invoice_payments;
