-- Plan 13: a customer block has a reason category and may end by itself (blocked_until); every block and lift is kept, with who made it and why (append-only: the
-- app role may only add rows).
-- Plan 14: a live tenant may also be frozen (read-only, no new charges) or banned; every change of status is kept with its category, its reason and who made it. A
-- change that needs a second operator (PAYRUDDER_TENANT_BAN_APPROVAL) waits as a "requested" row until another operator asks for the same.
-- The status CHECK is relaxed, not tightened: every existing row satisfies the new one. A release that predates it reads 'frozen' or 'banned' as an unknown status,
-- which it counts as active until it is replaced.

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

ALTER TABLE tenants DROP CONSTRAINT tenants_status_check,
  ADD CONSTRAINT tenants_status_check CHECK (status IN ('inactive', 'active', 'suspended', 'frozen', 'banned'));

CREATE TABLE tenant_status_events (
  id          uuid PRIMARY KEY,
  -- Not tenant_id: this is a global table about a live tenant (like go_live_audit), and the tables that carry a tenant_id are the tenant's own data.
  live_tenant_id uuid NOT NULL REFERENCES tenants (id),
  kind        text NOT NULL CHECK (kind IN ('changed', 'requested')),
  from_status text NOT NULL CHECK (from_status IN ('inactive', 'active', 'suspended', 'frozen', 'banned')),
  to_status   text NOT NULL CHECK (to_status IN ('inactive', 'active', 'suspended', 'frozen', 'banned')),
  category    text NOT NULL DEFAULT '' CHECK (category IN ('', 'nonpayment', 'fraud', 'abuse', 'legal', 'request', 'other')),
  reason      text NOT NULL DEFAULT '' CHECK (length(reason) <= 500),
  actor       text NOT NULL CHECK (length(actor) BETWEEN 1 AND 120),
  -- The second operator of a change that needed one.
  approved_by text NOT NULL DEFAULT '' CHECK (length(approved_by) <= 120),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tenant_status_events_by_tenant ON tenant_status_events (live_tenant_id, created_at DESC, id DESC);
GRANT SELECT, INSERT ON tenant_status_events TO plans_system;

-- +goose Down
DROP TABLE tenant_status_events;
ALTER TABLE tenants DROP CONSTRAINT tenants_status_check,
  ADD CONSTRAINT tenants_status_check CHECK (status IN ('inactive', 'active', 'suspended'));
DROP TABLE customer_block_events;
ALTER TABLE customers DROP CONSTRAINT customers_block_ends_later, DROP CONSTRAINT customers_block_extras_need_block, DROP COLUMN blocked_until, DROP COLUMN block_category;
