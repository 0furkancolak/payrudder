-- The help desk, third step (plan 12): tags on a ticket (the operator's own labels, never shown to the person who opened it), canned replies (titled texts the operators
-- share), and a fourth kind of change in the history. Global tables like the rest of the help desk: no tenant_id, only the system role reads them.

-- +goose Up
ALTER TABLE support_tickets ADD COLUMN tags text[] NOT NULL DEFAULT '{}' CHECK (cardinality(tags) <= 5);

ALTER TABLE support_events DROP CONSTRAINT support_events_kind_check,
  ADD CONSTRAINT support_events_kind_check CHECK (kind IN ('status', 'priority', 'assignee', 'tags'));

CREATE TABLE support_canned_replies (
  id          uuid PRIMARY KEY,
  title       text NOT NULL CHECK (length(title) BETWEEN 1 AND 100),
  body        text NOT NULL CHECK (length(body) BETWEEN 1 AND 10000),
  updated_by  text NOT NULL DEFAULT '' CHECK (length(updated_by) <= 200),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE, DELETE ON support_canned_replies TO plans_system;

-- +goose Down
DROP TABLE support_canned_replies;
DELETE FROM support_events WHERE kind = 'tags';
ALTER TABLE support_events DROP CONSTRAINT support_events_kind_check,
  ADD CONSTRAINT support_events_kind_check CHECK (kind IN ('status', 'priority', 'assignee'));
ALTER TABLE support_tickets DROP COLUMN tags;
