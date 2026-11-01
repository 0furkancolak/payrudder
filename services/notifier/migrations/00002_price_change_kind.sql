-- The price-change notice (subscription.price-change.v1) is a fourth thing that asks for an email, and its consumer records it as kind 'price_change'. The baseline
-- check allowed only three kinds, so the first notice failed on insert and the consumer retried it for ever: no price-change mail was ever sent.

-- +goose Up
ALTER TABLE sent_notifications DROP CONSTRAINT sent_notifications_kind_check,
  ADD CONSTRAINT sent_notifications_kind_check CHECK (kind IN ('command', 'limit', 'trial', 'price_change'));

-- +goose Down
-- The old check refuses these rows, so they go first (the notices are not sent again unless their facts are replayed).
DELETE FROM sent_notifications WHERE kind = 'price_change';
ALTER TABLE sent_notifications DROP CONSTRAINT sent_notifications_kind_check,
  ADD CONSTRAINT sent_notifications_kind_check CHECK (kind IN ('command', 'limit', 'trial'));
