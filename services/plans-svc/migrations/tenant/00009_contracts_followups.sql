-- Contracts, the follow-ups (docs/plans/21-contracts.md), the tenant schema's copy of ../00011_contracts_followups.sql: the same columns, constraint, table, policy and
-- privileges.

-- +goose Up
ALTER TABLE contract_settings
  -- full: accepting a withdrawal asks billing for a refund of the invoice; none: the seller refunds by hand. Either way the seller decides first.
  ADD COLUMN withdrawal_refund     text    NOT NULL DEFAULT 'full' CHECK (withdrawal_refund IN ('full', 'none')),
  -- Days before a renewal that the customer is mailed the notice; 0 is off.
  ADD COLUMN renewal_notice_days   integer NOT NULL DEFAULT 7 CHECK (renewal_notice_days BETWEEN 0 AND 60),
  -- Language to the seller's own words on how to cancel.
  ADD COLUMN renewal_cancel_texts  jsonb   NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(renewal_cancel_texts) = 'object'),
  ADD COLUMN withdrawal_function   boolean NOT NULL DEFAULT true;

-- The refund of an accepted withdrawal. credit_note_id belongs to billing, so there is no foreign key to it. 'requesting' is the claim of one caller; it is taken over when it
-- has stood for refund_at + 5 minutes without an answer.
ALTER TABLE contract_withdrawals
  ADD COLUMN credit_note_id uuid,
  ADD COLUMN refund_state   text NOT NULL DEFAULT 'none' CHECK (refund_state IN ('none', 'requesting', 'requested', 'outside', 'failed')),
  ADD COLUMN refund_note    text NOT NULL DEFAULT '' CHECK (length(refund_note) <= 1000),
  ADD COLUMN refund_at      timestamptz;

-- An acceptance made on the customer portal (a new material version is published between two payments).
ALTER TABLE contract_acceptances DROP CONSTRAINT contract_acceptances_context_kind_check;
ALTER TABLE contract_acceptances ADD CONSTRAINT contract_acceptances_context_kind_check CHECK (context_kind IN ('hosted_invoice', 'portal', 'api'));

-- One renewal notice per customer and renewal: the job inserts the row in the transaction that queues the mail, and ON CONFLICT DO NOTHING makes a second pass or a second
-- instance a no-op. It is also the stored copy of what the customer was told (price, date, the seller's words on cancelling).
CREATE TABLE contract_renewal_notices (
  tenant_id       uuid NOT NULL,
  customer_id     uuid NOT NULL,
  renews_at       timestamptz NOT NULL,
  subscription_id uuid NOT NULL,
  price_minor     bigint NOT NULL CHECK (price_minor >= 0),
  currency        char(3) NOT NULL,
  cancel_text     text NOT NULL DEFAULT '',
  notified_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, customer_id, renews_at),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id)
);
ALTER TABLE contract_renewal_notices ENABLE ROW LEVEL SECURITY;
ALTER TABLE contract_renewal_notices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON contract_renewal_notices USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT ON contract_renewal_notices TO plans_app;
-- plans_system: the job lists across tenants, and a sandbox reset deletes the notices with the customers they name.
GRANT SELECT, DELETE ON contract_renewal_notices TO plans_system;

-- +goose Down
DROP TABLE contract_renewal_notices;
ALTER TABLE contract_acceptances DROP CONSTRAINT contract_acceptances_context_kind_check;
ALTER TABLE contract_acceptances ADD CONSTRAINT contract_acceptances_context_kind_check CHECK (context_kind IN ('hosted_invoice', 'api'));
ALTER TABLE contract_withdrawals DROP COLUMN refund_at, DROP COLUMN refund_note, DROP COLUMN refund_state, DROP COLUMN credit_note_id;
ALTER TABLE contract_settings DROP COLUMN withdrawal_function, DROP COLUMN renewal_cancel_texts, DROP COLUMN renewal_notice_days, DROP COLUMN withdrawal_refund;
