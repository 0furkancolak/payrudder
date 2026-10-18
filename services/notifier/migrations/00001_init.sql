-- notifier_db (docs/05-data-model.md#notifier_db): the dedup record of every email, and the Kafka offsets of the limit
-- alert consumer. Like payments_db it is touched only by its system role.
-- +goose Up
CREATE TABLE sent_notifications (
  dedup_key        text PRIMARY KEY,            -- command_id (billing) or LimitExceeded.event_id (limits)
  command_id       uuid,
  kind             text NOT NULL CHECK (kind IN ('command', 'limit')),
  template         text NOT NULL,
  recipient        text NOT NULL,
  status           text NOT NULL CHECK (status IN ('SENDING', 'SENT')),
  claimed_at       timestamptz NOT NULL DEFAULT now(),
  sent_at          timestamptz,
  smtp_message_id  text
);

CREATE TABLE kafka_offsets (
  consumer_group text NOT NULL,
  topic          text NOT NULL,
  partition      int  NOT NULL,
  next_offset    bigint NOT NULL,
  updated_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (consumer_group, topic, partition)
);

GRANT SELECT, INSERT, UPDATE ON sent_notifications, kafka_offsets TO notifier_system;
-- A claim whose email was certainly not sent is released (deleted) so that the retry starts clean.
GRANT DELETE ON sent_notifications TO notifier_system;
GRANT SELECT ON sent_notifications, kafka_offsets TO notifier_app;

-- +goose Down
DROP TABLE kafka_offsets, sent_notifications;
