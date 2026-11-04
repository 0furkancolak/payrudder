-- Plan 14: the operator can ban an account (an organisation: every live tenant of it then behaves as banned, derived on read, no tenant row is rewritten) and a person (a user:
-- no sign-in, no live session). The flag is a column; every ban and lift is kept in ban_events (global, append-only by privilege like tenant_status_events).
-- subject_id is an accounts.id or a users.id depending on subject, so it carries no foreign key.

-- +goose Up
ALTER TABLE accounts ADD COLUMN banned_at timestamptz;
ALTER TABLE users ADD COLUMN banned_at timestamptz;

CREATE TABLE ban_events (
  id          uuid PRIMARY KEY,
  subject     text NOT NULL CHECK (subject IN ('account', 'user')),
  subject_id  uuid NOT NULL,
  -- "requested" waits for a second operator (PAYRUDDER_TENANT_BAN_APPROVAL), only a ban does.
  kind        text NOT NULL CHECK (kind IN ('banned', 'lifted', 'requested')),
  category    text NOT NULL DEFAULT '' CHECK (category IN ('', 'nonpayment', 'fraud', 'abuse', 'legal', 'request', 'other')),
  reason      text NOT NULL DEFAULT '' CHECK (length(reason) <= 500),
  actor       text NOT NULL CHECK (length(actor) BETWEEN 1 AND 120),
  approved_by text NOT NULL DEFAULT '' CHECK (length(approved_by) <= 120),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ban_events_by_subject ON ban_events (subject, subject_id, created_at DESC, id DESC);
GRANT SELECT, INSERT ON ban_events TO plans_system;

-- +goose Down
DROP TABLE ban_events;
ALTER TABLE users DROP COLUMN banned_at;
ALTER TABLE accounts DROP COLUMN banned_at;
