-- The mail that follows from a customer's agreements (docs/plans/21-contracts.md) asks for an email in three more ways, and each consumer records its own kind: 'contract' (the
-- copy of what the customer accepted, contract.accepted.v1), 'renewal' (the renewal notice, subscription.renewal-notice.v1) and 'withdrawal' (the acknowledgement of a
-- withdrawal, contract.withdrawal-received.v1). The check allowed four kinds only, so the first of each failed on insert and its consumer retried it for ever: the copy of an
-- accepted agreement was never sent either (the same mistake as 00002).

-- +goose Up
ALTER TABLE sent_notifications DROP CONSTRAINT sent_notifications_kind_check,
  ADD CONSTRAINT sent_notifications_kind_check CHECK (kind IN ('command', 'limit', 'trial', 'price_change', 'contract', 'renewal', 'withdrawal'));

-- +goose Down
-- The old check refuses these rows, so they go first (the mails are not sent again unless their facts are replayed).
DELETE FROM sent_notifications WHERE kind IN ('contract', 'renewal', 'withdrawal');
ALTER TABLE sent_notifications DROP CONSTRAINT sent_notifications_kind_check,
  ADD CONSTRAINT sent_notifications_kind_check CHECK (kind IN ('command', 'limit', 'trial', 'price_change'));
