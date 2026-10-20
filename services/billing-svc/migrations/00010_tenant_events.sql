-- Phase 23: events for the tenant's own systems (invoice.paid, credit_note.created, ...). They are written here in the same transaction as the change they
-- announce and handed to plans-svc, which owns the tenant's webhook endpoints and delivers them. A row is removed once plans-svc has recorded it; the event
-- id makes a repeat harmless.
-- +goose Up
CREATE TABLE tenant_event_outbox (
  tenant_id   uuid NOT NULL,
  event_id    uuid NOT NULL,
  type        text NOT NULL,
  data        jsonb NOT NULL,
  occurred_at timestamptz NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, event_id)
);
CREATE INDEX tenant_event_outbox_by_age ON tenant_event_outbox (created_at);
ALTER TABLE tenant_event_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_event_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_event_outbox USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, DELETE ON tenant_event_outbox TO billing_system;

-- +goose Down
DROP TABLE tenant_event_outbox;
