-- notifier_db (docs/05-data-model.md#notifier_db): the dedup record of every email, and the Kafka offsets of the limit
-- alert consumer. Like payments_db it is touched only by its system role.
-- This is the baseline: it was written once, before the first release, from the schema the pre-release migrations had built, and it starts the history.
-- Every change from here on is a new numbered migration (00002_...), never an edit of this file.

-- +goose Up
-- kind: the three things that ask for an email: a billing command, a limit alert and the trial-ending reminder (subscription.trial-will-end.v1).
CREATE TABLE sent_notifications (
  dedup_key        text PRIMARY KEY,            -- command_id (billing) or LimitExceeded.event_id (limits)
  command_id       uuid,
  kind             text NOT NULL CHECK (kind IN ('command', 'limit', 'trial')),
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
