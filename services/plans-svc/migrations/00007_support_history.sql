-- The help desk, second step (plan 12): a history of what changed on a ticket, the time zone its first-answer deadline counts business hours in, and whether the
-- person who opened it has seen the latest answer. Global tables like the rest of the help desk: no tenant_id, only the system role reads them.
--
-- sla_zone: the IANA zone of the ticket's app (else the oldest app of the account, else UTC), fixed when the ticket is opened so a later change of priority moves the
-- deadline the same way the first one was set. opener_seen_at is NULL while the opener has an answer (or a status change by the operator) they have not read; a
-- ticket starts seen (the opener wrote it), and the tickets that exist now count as seen so nobody opens the console to a wall of "new".

-- +goose Up
ALTER TABLE support_tickets
  ADD COLUMN sla_zone text NOT NULL DEFAULT 'UTC' CHECK (length(sla_zone) BETWEEN 1 AND 64),
  ADD COLUMN opener_seen_at timestamptz DEFAULT now();

-- One row per change of status, priority or assignee, never edited or deleted (plans_system has no UPDATE or DELETE on it). actor_kind system is the job that closes
-- tickets nobody answered for days.
CREATE TABLE support_events (
  id          uuid PRIMARY KEY,
  ticket_id   uuid NOT NULL REFERENCES support_tickets (id),
  kind        text NOT NULL CHECK (kind IN ('status', 'priority', 'assignee')),
  actor_kind  text NOT NULL CHECK (actor_kind IN ('customer', 'operator', 'system')),
  actor_name  text NOT NULL DEFAULT '' CHECK (length(actor_name) <= 200),
  from_value  text NOT NULL DEFAULT '' CHECK (length(from_value) <= 100),
  to_value    text NOT NULL DEFAULT '' CHECK (length(to_value) <= 100),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX support_events_by_ticket ON support_events (ticket_id, created_at, id);

GRANT SELECT, INSERT ON support_events TO plans_system;

-- +goose Down
DROP TABLE support_events;
ALTER TABLE support_tickets DROP COLUMN opener_seen_at, DROP COLUMN sla_zone;
