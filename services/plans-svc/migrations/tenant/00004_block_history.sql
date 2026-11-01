-- Plan 13: a customer block has a reason category and may end by itself (blocked_until); every block and lift is kept, with who made it and why (append-only: the
-- app role may only add rows). The twin of ../00005_block_and_tenant_status.sql for what has a tenant_id; the tenants table and its status events stay in the pooled
-- database.

-- +goose Up
ALTER TABLE customers
  ADD COLUMN block_category text NOT NULL DEFAULT '' CHECK (block_category IN ('', 'fraud', 'chargeback', 'abuse', 'request', 'other')),
  ADD COLUMN blocked_until timestamptz,
  ADD CONSTRAINT customers_block_extras_need_block CHECK (blocked_at IS NOT NULL OR (block_category = '' AND blocked_until IS NULL)),
  ADD CONSTRAINT customers_block_ends_later CHECK (blocked_until IS NULL OR blocked_until > blocked_at);

CREATE TABLE customer_block_events (
  tenant_id     uuid NOT NULL,
  id            uuid NOT NULL,
  customer_id   uuid NOT NULL,
  action        text NOT NULL CHECK (action IN ('blocked', 'unblocked')),
  category      text NOT NULL DEFAULT '' CHECK (category IN ('', 'fraud', 'chargeback', 'abuse', 'request', 'other')),
  reason        text NOT NULL DEFAULT '' CHECK (length(reason) <= 500),
  blocked_until timestamptz,
  actor         text NOT NULL DEFAULT '' CHECK (length(actor) <= 120),
  created_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id)
);
CREATE INDEX customer_block_events_by_customer ON customer_block_events (tenant_id, customer_id, created_at DESC, id DESC);

ALTER TABLE customer_block_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_block_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_block_events USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT ON customer_block_events TO plans_app;
-- A sandbox reset deletes the customers, and their history first.
GRANT SELECT, DELETE ON customer_block_events TO plans_system;

-- +goose Down
DROP TABLE customer_block_events;
ALTER TABLE customers DROP CONSTRAINT customers_block_ends_later, DROP CONSTRAINT customers_block_extras_need_block, DROP COLUMN blocked_until, DROP COLUMN block_category;
