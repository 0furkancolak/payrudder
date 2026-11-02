-- Files on the messages of a help desk ticket (plan 12). A global table like the rest of the help desk: it holds no tenant's rows (storage_tenant_id says which
-- tenant's storage in assets-svc holds the file, the first tenant of the ticket's account: the name is not tenant_id on purpose, so that no row-level-security or
-- sandbox-reset rule takes this for a tenant table), only the system role reads it.
--
-- The bytes are in assets-svc (purpose support_attachment); this row is the record that ties them to a message, with the name and type the person saw. It is never
-- edited or deleted (plans_system has INSERT and SELECT only). content_type and the size follow assets-svc's rule for the purpose; the count per ticket and the bytes
-- per account are checked by the module under the ticket's lock.

-- +goose Up
CREATE TABLE support_attachments (
  id            uuid PRIMARY KEY,
  ticket_id     uuid NOT NULL REFERENCES support_tickets (id),
  message_id    uuid NOT NULL REFERENCES support_messages (id),
  storage_tenant_id uuid NOT NULL REFERENCES tenants (id),
  asset_id      uuid NOT NULL UNIQUE,
  filename      text NOT NULL CHECK (length(filename) BETWEEN 1 AND 255),
  content_type  text NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg', 'application/pdf', 'text/plain')),
  size_bytes    bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 5242880),
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX support_attachments_by_ticket ON support_attachments (ticket_id, created_at, id);
CREATE INDEX support_attachments_by_message ON support_attachments (message_id);

GRANT SELECT, INSERT ON support_attachments TO plans_system;

-- +goose Down
DROP TABLE support_attachments;
