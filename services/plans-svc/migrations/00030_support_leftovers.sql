-- Plan 12, leftovers. Help-desk tables are account-level (no tenant_id, no twin in tenant/), run as plans_system.
--  - sla_tier / sla_app_id: the plan of the app and the app whose zone and days off the first-answer deadline was counted for, kept so a change of priority counts it the
--    same way.
--  - support_holidays: the days off of an app; the clock of its tickets does not run on them.
--  - merged_into: a ticket whose messages were moved into another of the same person. The messages and their files move (UPDATE of ticket_id, the one column the
--    system role may change on them); nothing else about a message or a file ever changes.
--  - rating: the person's 1 to 5 and comment after the ticket was solved.
--  - tsv: the words of a message, for the search over bodies ('simple': no stemming, any language).
--  - support_ticket_links: pointers from a ticket to a customer, an invoice or a subscription of one of the account's apps. Only the pointer is kept.

-- +goose Up
ALTER TABLE support_tickets
  ADD COLUMN sla_tier        text NOT NULL DEFAULT 'free' CHECK (sla_tier IN ('free', 'standard', 'enterprise')),
  ADD COLUMN sla_app_id      uuid REFERENCES apps (id),
  ADD COLUMN merged_into     uuid REFERENCES support_tickets (id),
  ADD COLUMN rating          smallint CHECK (rating BETWEEN 1 AND 5),
  ADD COLUMN rating_comment  text NOT NULL DEFAULT '' CHECK (length(rating_comment) <= 1000),
  ADD COLUMN rated_at        timestamptz,
  ADD CONSTRAINT support_rating_has_time CHECK ((rating IS NULL) = (rated_at IS NULL)),
  ADD CONSTRAINT support_not_merged_into_itself CHECK (merged_into IS NULL OR merged_into <> id);
CREATE INDEX support_tickets_merged_into ON support_tickets (merged_into) WHERE merged_into IS NOT NULL;

ALTER TABLE support_messages ADD COLUMN tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', body)) STORED;
CREATE INDEX support_messages_search ON support_messages USING gin (tsv);
GRANT UPDATE (ticket_id) ON support_messages, support_attachments TO plans_system;

ALTER TABLE support_events DROP CONSTRAINT support_events_kind_check,
  ADD CONSTRAINT support_events_kind_check CHECK (kind IN ('status', 'priority', 'assignee', 'tags', 'merge'));

CREATE TABLE support_holidays (
  app_id  uuid NOT NULL REFERENCES apps (id),
  day     date NOT NULL,
  name    text NOT NULL DEFAULT '' CHECK (length(name) <= 100),
  PRIMARY KEY (app_id, day)
);
GRANT SELECT, INSERT, DELETE ON support_holidays TO plans_system;

CREATE TABLE support_ticket_links (
  id               uuid PRIMARY KEY,
  ticket_id        uuid NOT NULL REFERENCES support_tickets (id),
  link_tenant_id   uuid NOT NULL REFERENCES tenants (id),
  kind             text NOT NULL CHECK (kind IN ('customer', 'invoice', 'subscription')),
  ref              text NOT NULL CHECK (length(ref) BETWEEN 1 AND 200),
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (ticket_id, link_tenant_id, kind, ref)
);
CREATE INDEX support_ticket_links_by_ticket ON support_ticket_links (ticket_id, created_at, id);
GRANT SELECT, INSERT, DELETE ON support_ticket_links TO plans_system;

-- +goose Down
DROP TABLE support_ticket_links;
DROP TABLE support_holidays;
DELETE FROM support_events WHERE kind = 'merge';
ALTER TABLE support_events DROP CONSTRAINT support_events_kind_check,
  ADD CONSTRAINT support_events_kind_check CHECK (kind IN ('status', 'priority', 'assignee', 'tags'));
REVOKE UPDATE (ticket_id) ON support_messages, support_attachments FROM plans_system;
DROP INDEX support_messages_search;
ALTER TABLE support_messages DROP COLUMN tsv;
DROP INDEX support_tickets_merged_into;
ALTER TABLE support_tickets
  DROP CONSTRAINT support_not_merged_into_itself, DROP CONSTRAINT support_rating_has_time,
  DROP COLUMN rated_at, DROP COLUMN rating_comment, DROP COLUMN rating, DROP COLUMN merged_into, DROP COLUMN sla_app_id, DROP COLUMN sla_tier;
