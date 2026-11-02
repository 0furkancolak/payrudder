-- Plan 22 (disputes and chargebacks): a customer questions a payment with their bank (Stripe's charge.dispute.*, Paddle's chargeback adjustments). One row per case, kept
-- where it stands now (outcome open, won or lost); the ledger holds each step of it as an append-only row of kind 'dispute'. payment_ref is the payment that is disputed,
-- as the ledger holds it (a Stripe PaymentIntent id, a Paddle transaction id); invoice_id is that payment's invoice, empty for a payment Payrudder did not make.
-- No row-level security, like every table of payments_db (the head of 00001_init.sql): only payments_system touches it, and it deletes only for the sandbox reset.
-- +goose Up
CREATE TABLE payment_disputes (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id       uuid NOT NULL,
  environment     text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  provider        text NOT NULL,
  dispute_ref     text NOT NULL,
  payment_ref     text NOT NULL,
  invoice_id      uuid,
  amount_minor    bigint NOT NULL CHECK (amount_minor >= 0),
  currency        char(3) NOT NULL,
  outcome         text NOT NULL CHECK (outcome IN ('open', 'won', 'lost')),
  formal          boolean NOT NULL,         -- a chargeback proper, which takes the money back; false is an inquiry or a warning
  provider_status text NOT NULL DEFAULT '', -- the provider's own word (needs_response, under_review, won, ...)
  reason          text NOT NULL DEFAULT '',
  evidence_due_at timestamptz,
  opened_at       timestamptz NOT NULL,
  closed_at       timestamptz,
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, provider, dispute_ref)
);
CREATE INDEX payment_disputes_by_invoice ON payment_disputes (tenant_id, invoice_id);
CREATE INDEX payment_disputes_by_outcome ON payment_disputes (tenant_id, outcome, opened_at DESC);
GRANT SELECT, INSERT, UPDATE, DELETE ON payment_disputes TO payments_system;

-- +goose Down
DROP TABLE payment_disputes;
