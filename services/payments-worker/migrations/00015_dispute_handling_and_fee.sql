-- What a seller can do about a dispute inside Payrudder, with no provider account (plans 08 and 13): a note, a mark that it has been handled, and the fee the dispute cost.
-- A case's evidence is still answered at the provider (Payrudder submits none: docs/plans/08). The mark is the seller's own and changes nothing at the provider or in the ledger.
-- Like every table of payments_db these hold ids, amounts and notes only and have no row-level security (only payments_system touches them).
-- +goose Up

-- The fee the provider took for the case, as its notification says (Stripe's balance transactions of the dispute, Paddle's chargeback fee of the payout totals; both
-- UNVERIFIED against an account), in the currency it settles in: absent when no notification said. handled_at and handled_by are the seller's mark.
ALTER TABLE payment_disputes
  ADD COLUMN fee_minor    bigint CHECK (fee_minor IS NULL OR fee_minor > 0),
  ADD COLUMN fee_currency char(3),
  ADD COLUMN handled_at   timestamptz,
  ADD COLUMN handled_by   text NOT NULL DEFAULT '';

-- What the seller did with a case, in order: a note, a mark that it was handled, the mark lifted. Append-only (no UPDATE is granted, and a delete is refused as for the
-- reconciliation's events, with the same function): a correction is a new row. actor is who the gateway says.
CREATE TABLE payment_dispute_events (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id   uuid NOT NULL,
  environment text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  dispute_id  bigint NOT NULL REFERENCES payment_disputes (id),
  action      text NOT NULL CHECK (action IN ('note', 'handled', 'unhandled')),
  note        text NOT NULL DEFAULT '' CHECK (char_length(note) <= 2000),
  actor       text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payment_dispute_events_by_dispute ON payment_dispute_events (tenant_id, dispute_id, id DESC);
CREATE TRIGGER payment_dispute_events_no_delete BEFORE DELETE ON payment_dispute_events FOR EACH ROW EXECUTE FUNCTION reconciliation_events_no_delete();
GRANT SELECT, INSERT, DELETE ON payment_dispute_events TO payments_system;

-- The fee of a dispute is a row of the ledger of its own (kind dispute_fee, the provider reference of the case with ":fee"): money the provider took from the seller. The
-- constraint is added NOT VALID and validated after, which does not hold the ledger's writers up for the length of a scan.
ALTER TABLE payment_ledger DROP CONSTRAINT payment_ledger_kind_check;
ALTER TABLE payment_ledger ADD CONSTRAINT payment_ledger_kind_check CHECK (kind IN ('charge', 'refund', 'dispute', 'dispute_fee', 'payout')) NOT VALID;
ALTER TABLE payment_ledger VALIDATE CONSTRAINT payment_ledger_kind_check;

-- +goose Down
ALTER TABLE payment_ledger DROP CONSTRAINT payment_ledger_kind_check;
ALTER TABLE payment_ledger ADD CONSTRAINT payment_ledger_kind_check CHECK (kind IN ('charge', 'refund', 'dispute', 'payout'));
DROP TABLE payment_dispute_events;
ALTER TABLE payment_disputes DROP COLUMN handled_by, DROP COLUMN handled_at, DROP COLUMN fee_currency, DROP COLUMN fee_minor;
