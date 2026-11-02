-- Contracts (docs/plans/21-contracts.md): the result of the refund the credit note of a withdrawal asked the provider for. credit_note.refunded and credit_note.refund_failed, which
-- billing-svc hands to plans-svc as tenant events, are copied onto the withdrawal whose credit_note_id names the note: 'refunded' (the money went back to the card) and
-- 'refund_failed' (the credit note exists, the provider did not return the money: retry the note's refund in billing, do not make another note). 'failed' stays what it was, billing
-- refusing or not answering the request.

-- +goose Up
ALTER TABLE contract_withdrawals DROP CONSTRAINT contract_withdrawals_refund_state_check;
ALTER TABLE contract_withdrawals ADD CONSTRAINT contract_withdrawals_refund_state_check
  CHECK (refund_state IN ('none', 'requesting', 'requested', 'outside', 'failed', 'refunded', 'refund_failed'));

-- +goose Down
UPDATE contract_withdrawals SET refund_state = 'requested' WHERE refund_state IN ('refunded', 'refund_failed');
ALTER TABLE contract_withdrawals DROP CONSTRAINT contract_withdrawals_refund_state_check;
ALTER TABLE contract_withdrawals ADD CONSTRAINT contract_withdrawals_refund_state_check CHECK (refund_state IN ('none', 'requesting', 'requested', 'outside', 'failed'));
