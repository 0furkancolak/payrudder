-- The suppression list (plan 11): addresses a tenant must not be mailed any more because they bounced for good, complained, or the operator said so.
-- It is fed by the notifier's mail-event endpoint and read before every live send. A bounce or complaint names the provider's message id, not a tenant, so
-- the row of sent_notifications that holds that id also holds the tenant it was sent for (tenant_id is NULL for mail sent before this migration and for mail
-- that has no tenant; those can still be matched by id but have nothing to suppress).

-- +goose Up
ALTER TABLE sent_notifications ADD COLUMN tenant_id uuid;
CREATE INDEX sent_notifications_by_message_id ON sent_notifications (smtp_message_id) WHERE smtp_message_id IS NOT NULL;

CREATE TABLE mail_suppressions (
  tenant_id   uuid NOT NULL,
  address     text NOT NULL CHECK (address = lower(address)),   -- compared case-insensitively: the list holds lower case
  reason      text NOT NULL CHECK (reason IN ('bounce', 'complaint', 'manual')),
  source      text NOT NULL DEFAULT '',                          -- the provider message id of the mail that bounced, or the operator's name
  detail      text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, address)
);

GRANT SELECT, INSERT, DELETE ON mail_suppressions TO notifier_system;
GRANT SELECT ON mail_suppressions TO notifier_app;

-- +goose Down
DROP TABLE mail_suppressions;
DROP INDEX sent_notifications_by_message_id;
ALTER TABLE sent_notifications DROP COLUMN tenant_id;
