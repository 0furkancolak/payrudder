-- The credits of a credit-pack invoice that was voided or credited are taken back (docs/plans/22, review finding 4). The ledger stays append-only: a take-back is a row of
-- its own kind, "revoke", with a negative amount (a lot is never edited in place except for its remaining credits, as for a consume). A customer who had already spent the
-- credits has nothing left to take: that revoke is a row of amount 0, whose reason says how many credits could not be taken back, so the shortfall is on record and the
-- repeat of the same event (the row's idempotency key) finds it. Only a revoke may be 0. The rule that a positive amount is a grant (credit_ledger_check) is unchanged.
-- +goose Up
ALTER TABLE credit_ledger DROP CONSTRAINT credit_ledger_kind_check;
ALTER TABLE credit_ledger DROP CONSTRAINT credit_ledger_amount_check;
ALTER TABLE credit_ledger ADD CONSTRAINT credit_ledger_kind_check CHECK (kind IN ('grant', 'consume', 'expire', 'revoke'));
ALTER TABLE credit_ledger ADD CONSTRAINT credit_ledger_amount_check CHECK (amount <> 0 OR kind = 'revoke');

-- +goose Down
ALTER TABLE credit_ledger DROP CONSTRAINT credit_ledger_amount_check;
ALTER TABLE credit_ledger DROP CONSTRAINT credit_ledger_kind_check;
ALTER TABLE credit_ledger ADD CONSTRAINT credit_ledger_amount_check CHECK (amount <> 0);
ALTER TABLE credit_ledger ADD CONSTRAINT credit_ledger_kind_check CHECK (kind IN ('grant', 'consume', 'expire'));
