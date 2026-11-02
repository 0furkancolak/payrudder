-- The notice to a blocked customer (customer.block-notice.v1, plan 13) records its own kind. sent_notifications_kind_check lists the kinds, so the first notice would fail on
-- insert and its consumer would retry it for ever (the mistake of 00002 and 00004).

-- +goose Up
ALTER TABLE sent_notifications DROP CONSTRAINT sent_notifications_kind_check,
  ADD CONSTRAINT sent_notifications_kind_check CHECK (kind IN ('command', 'limit', 'trial', 'price_change', 'contract', 'renewal', 'withdrawal', 'customer_notice'));

-- +goose Down
DELETE FROM sent_notifications WHERE kind = 'customer_notice';
ALTER TABLE sent_notifications DROP CONSTRAINT sent_notifications_kind_check,
  ADD CONSTRAINT sent_notifications_kind_check CHECK (kind IN ('command', 'limit', 'trial', 'price_change', 'contract', 'renewal', 'withdrawal'));
