-- The sweep that asks for the status of a payment that is awaited (a bank transfer) also asks for one that waits for the customer to authenticate (PAYMENT_ACTION_REQUIRED, 3-D
-- Secure): the partial index it reads covers both states.
-- +goose Up
DROP INDEX saga_polls;
CREATE INDEX saga_polls ON invoice_sagas (next_attempt_at) WHERE state IN ('PAYMENT_PENDING', 'PAYMENT_ACTION_REQUIRED');

-- +goose Down
DROP INDEX saga_polls;
CREATE INDEX saga_polls ON invoice_sagas (next_attempt_at) WHERE state = 'PAYMENT_PENDING';
