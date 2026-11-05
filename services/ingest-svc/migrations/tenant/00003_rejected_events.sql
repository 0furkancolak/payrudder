-- The rejected-event viewer (docs/plans/18-quick-integration.md, work item 3), for a tenant's schema: the same as ../00003_rejected_events.sql.
--
-- POST /v1/events answers a rejected event with an error_code and a message, and forgets it. A developer who looks later cannot tell what went wrong, so each rejection is
-- written here, best effort and after the answer is known (a failed write never fails the request). The table is a bounded log: the last 14 days and at most 10 000 rows per
-- tenant (a tenant is one environment, ADR 0011), the oldest dropped by the writer and by the partition maintenance. It holds what the developer sent to identify the event (the meter
-- code, the customer id, the event time, the idempotency key; cut to a length) and what the API answered. It never holds the properties or the quantity.
-- +goose Up
CREATE TABLE rejected_events (
  id                   uuid        NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
  tenant_id            uuid        NOT NULL,
  error_code           text        NOT NULL,
  error_message        text        NOT NULL,
  meter_code           text        NOT NULL,
  customer_external_id text        NOT NULL,
  event_time           timestamptz,
  idempotency_key      text        NOT NULL,
  received_at          timestamptz NOT NULL DEFAULT now()
);
-- The list is newest first within a tenant; the trim by age and the summary read the same order.
CREATE INDEX rejected_events_tenant_received ON rejected_events (tenant_id, received_at DESC, id DESC);
CREATE INDEX rejected_events_received ON rejected_events (received_at);

ALTER TABLE rejected_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE rejected_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON rejected_events
  USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

-- The handlers write, read and trim their own tenant's rows; resetting a sandbox and the age sweep (ingest_system, every tenant) delete.
GRANT SELECT, INSERT, DELETE ON rejected_events TO ingest_app;
GRANT SELECT, DELETE ON rejected_events TO ingest_system;

-- +goose Down
DROP TABLE rejected_events;
