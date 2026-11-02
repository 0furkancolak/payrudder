-- The dunning sweep also picks up the first charge of an invoice the payment gate held (PAYMENT_HELD, docs/plans/14): the partial index it reads covers both states.
-- +goose Up
DROP INDEX saga_dunning;
CREATE INDEX saga_dunning ON invoice_sagas (next_attempt_at) WHERE state IN ('PAYMENT_DECLINED', 'PAYMENT_HELD');

-- +goose Down
DROP INDEX saga_dunning;
CREATE INDEX saga_dunning ON invoice_sagas (next_attempt_at) WHERE state = 'PAYMENT_DECLINED';
