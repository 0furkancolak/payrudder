-- Plan 13: blocklists. The twin of ../00028_blocklists.sql for what has a tenant_id (the operator's platform_blocklist stays in the pooled database). So that a person the seller has had to turn away cannot come back under another customer id. Two lists, the same entries:
--  * customer_blocklist: the app's own (tenant-scoped). A new customer, or a customer's new e-mail address or tax id, that matches an entry is refused.
--  * platform_blocklist: the operator's, across every app (a global table like tenant_status_events: no tenant_id, and no grant for plans_app; plans-svc reads it on the
--    system pool, so an app in a cell, which cannot see the pooled database from its own transaction, is held to it too).
-- An entry is a kind and a normalised value: an e-mail address in lower case, a tax id without spaces and dashes in capitals, an external customer id as it is. The note is
-- the owner's own and is never shown to the person it is about. Each list is capped at 1000 entries by the command, so the lists are never paged.

-- +goose Up
CREATE TABLE customer_blocklist (
  tenant_id  uuid NOT NULL,
  id         uuid NOT NULL,
  kind       text NOT NULL CHECK (kind IN ('email', 'tax_id', 'external_id')),
  value      text NOT NULL CHECK (length(value) BETWEEN 1 AND 320),
  note       text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  created_by text NOT NULL DEFAULT '' CHECK (length(created_by) <= 120),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, kind, value)
);
ALTER TABLE customer_blocklist ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_blocklist FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_blocklist USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, DELETE ON customer_blocklist TO plans_app;
-- The list is the seller's configuration: a sandbox reset keeps it, like the coupons and the webhook endpoints.
GRANT SELECT ON customer_blocklist TO plans_system;

-- +goose Down
DROP TABLE customer_blocklist;
