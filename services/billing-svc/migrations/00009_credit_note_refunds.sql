-- Phase 23: a credit note may return its amount to the customer's card instead of their credit balance. The refund is a RefundPayment command whose
-- reply is matched by command id; billing's tick re-sends it when no reply comes (the provider dedups on the idempotency key).
-- +goose Up
ALTER TABLE credit_notes
  ADD COLUMN disposition          text NOT NULL DEFAULT 'credit' CHECK (disposition IN ('credit', 'refund')),
  ADD COLUMN refund_state         text NOT NULL DEFAULT 'none' CHECK (refund_state IN ('none', 'requested', 'refunded', 'failed')),
  ADD COLUMN refund_attempt       int NOT NULL DEFAULT 0,
  ADD COLUMN refund_run_from      int NOT NULL DEFAULT 0,   -- the attempt a manual retry started from: a run is at most 3 attempts
  ADD COLUMN refund_generation    int NOT NULL DEFAULT 0,   -- part of the idempotency key; a retry after a rejection starts a new one
  ADD COLUMN refund_command_id    uuid,
  ADD COLUMN refund_requested_at  timestamptz,
  ADD COLUMN refund_ref           text,
  ADD COLUMN refund_error         text,
  ADD CONSTRAINT refund_only_for_refunds CHECK (disposition = 'refund' OR refund_state = 'none');
CREATE UNIQUE INDEX credit_notes_refund_command ON credit_notes (refund_command_id) WHERE refund_command_id IS NOT NULL;
CREATE INDEX credit_notes_refund_pending ON credit_notes (refund_requested_at) WHERE refund_state = 'requested';
GRANT UPDATE (refund_state, refund_attempt, refund_run_from, refund_generation, refund_command_id, refund_requested_at, refund_ref, refund_error) ON credit_notes TO billing_system;

-- +goose Down
DROP INDEX credit_notes_refund_pending, credit_notes_refund_command;
ALTER TABLE credit_notes DROP CONSTRAINT refund_only_for_refunds, DROP COLUMN refund_error, DROP COLUMN refund_ref, DROP COLUMN refund_requested_at,
  DROP COLUMN refund_command_id, DROP COLUMN refund_generation, DROP COLUMN refund_run_from, DROP COLUMN refund_attempt, DROP COLUMN refund_state, DROP COLUMN disposition;
