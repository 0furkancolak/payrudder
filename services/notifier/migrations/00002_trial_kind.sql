-- The trial-ending reminder (subscription.trial-will-end.v1) is a third thing that asks for an email, next to a billing command and a limit alert.

-- +goose Up
ALTER TABLE sent_notifications DROP CONSTRAINT sent_notifications_kind_check;
ALTER TABLE sent_notifications ADD CONSTRAINT sent_notifications_kind_check CHECK (kind IN ('command', 'limit', 'trial'));

-- +goose Down
ALTER TABLE sent_notifications DROP CONSTRAINT sent_notifications_kind_check;
ALTER TABLE sent_notifications ADD CONSTRAINT sent_notifications_kind_check CHECK (kind IN ('command', 'limit'));
