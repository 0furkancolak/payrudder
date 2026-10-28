-- The help desk between the people who run apps and the operator (SupportService). Global tables like users: no tenant_id, only the system role reads them.
-- A ticket belongs to an account (every member of it sees the ticket), optionally to one of its apps. first_response_due follows the priority and is set by the
-- module; first_responded_at is set by the operator's first public answer (an internal note does not count).

-- +goose Up
CREATE TABLE support_tickets (
  id                  uuid PRIMARY KEY,
  number              bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
  account_id          uuid NOT NULL REFERENCES accounts (id),
  app_id              uuid REFERENCES apps (id),
  opened_by           uuid NOT NULL REFERENCES users (id),
  subject             text NOT NULL CHECK (length(subject) BETWEEN 3 AND 200),
  category            text NOT NULL CHECK (category IN ('billing', 'integration', 'bug', 'question', 'other')),
  priority            text NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
  status              text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'pending', 'solved', 'closed')),
  assignee            text NOT NULL DEFAULT '' CHECK (length(assignee) <= 100),
  last_author         text NOT NULL DEFAULT 'customer' CHECK (last_author IN ('customer', 'operator')),
  first_response_due  timestamptz NOT NULL,
  first_responded_at  timestamptz,
  last_message_at     timestamptz NOT NULL DEFAULT now(),
  solved_at           timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX support_tickets_by_account ON support_tickets (account_id, last_message_at DESC);
CREATE INDEX support_tickets_by_activity ON support_tickets (last_message_at DESC);

CREATE TABLE support_messages (
  id               uuid PRIMARY KEY,
  ticket_id        uuid NOT NULL REFERENCES support_tickets (id),
  author_kind      text NOT NULL CHECK (author_kind IN ('customer', 'operator', 'system')),
  author_user_id   uuid REFERENCES users (id),
  author_name      text NOT NULL DEFAULT '' CHECK (length(author_name) <= 200),
  internal         boolean NOT NULL DEFAULT false,
  body             text NOT NULL CHECK (length(body) BETWEEN 1 AND 10000),
  created_at       timestamptz NOT NULL DEFAULT now(),
  -- Only the operator writes notes the person does not see.
  CONSTRAINT support_internal_is_operator CHECK (NOT internal OR author_kind = 'operator')
);
CREATE INDEX support_messages_by_ticket ON support_messages (ticket_id, created_at, id);

GRANT SELECT, INSERT, UPDATE ON support_tickets TO plans_system;
GRANT SELECT, INSERT ON support_messages TO plans_system;

-- +goose Down
DROP TABLE support_messages;
DROP TABLE support_tickets;
