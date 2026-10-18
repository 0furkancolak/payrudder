-- mockpsp_db: the mock payment provider's own database (a real PSP keeps its state somewhere else, not in Quotify's).
-- +goose Up
CREATE TABLE charges (
  idempotency_key text PRIMARY KEY,
  id              text NOT NULL UNIQUE,
  invoice_id      text NOT NULL,
  payment_method  text NOT NULL,
  amount_minor    bigint NOT NULL,
  currency        text NOT NULL,
  status          text NOT NULL CHECK (status IN ('succeeded', 'declined')),
  decline_code    text,
  requests        int NOT NULL DEFAULT 1,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX charges_by_invoice ON charges (invoice_id);

CREATE TABLE refunds (
  idempotency_key text PRIMARY KEY,
  id              text NOT NULL UNIQUE,
  invoice_id      text NOT NULL,
  charge_id       text,
  amount_minor    bigint NOT NULL,
  currency        text NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now()
);

-- Requests seen per idempotency key, whether or not they committed a charge (scenario tokens count attempts).
CREATE TABLE attempts (
  idempotency_key text PRIMARY KEY,
  requests        int NOT NULL
);

GRANT SELECT, INSERT, UPDATE ON charges, refunds, attempts TO mockpsp_app, mockpsp_system;

-- +goose Down
DROP TABLE attempts, refunds, charges;
