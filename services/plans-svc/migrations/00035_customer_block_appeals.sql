-- Plan 13: the appeal of a blocked customer. The customer states why the seller should lift the block (from the portal link the seller gave them); the seller (the app's
-- members with write rights) accepts, which lifts the block through the same path as the seller's own lift, or rejects with an answer. One open appeal per customer, so one
-- per block (a customer is blocked once at a time), enforced by the database. An appeal still open when the block is lifted by other means is closed ('closed').
-- The statement is the customer's words: untrusted, shown to the seller as text only. The twin for a tenant's own schema is tenant/00024.

-- +goose Up
CREATE TABLE customer_block_appeals (
  tenant_id   uuid NOT NULL,
  id          uuid NOT NULL,
  customer_id uuid NOT NULL,
  status      text NOT NULL CHECK (status IN ('open', 'accepted', 'rejected', 'closed')),
  statement   text NOT NULL CHECK (char_length(statement) BETWEEN 1 AND 2000),
  answer      text NOT NULL DEFAULT '' CHECK (char_length(answer) <= 1000),
  decided_by  text NOT NULL DEFAULT '' CHECK (char_length(decided_by) <= 120),
  created_at  timestamptz NOT NULL DEFAULT now(),
  decided_at  timestamptz,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
  CHECK ((status = 'open') = (decided_at IS NULL))
);
CREATE UNIQUE INDEX customer_block_appeals_one_open ON customer_block_appeals (tenant_id, customer_id) WHERE status = 'open';
CREATE INDEX customer_block_appeals_by_customer ON customer_block_appeals (tenant_id, customer_id, created_at DESC, id DESC);

ALTER TABLE customer_block_appeals ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_block_appeals FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_block_appeals USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, UPDATE ON customer_block_appeals TO plans_app;
-- A sandbox reset deletes the customers, and their appeals first.
GRANT SELECT, DELETE ON customer_block_appeals TO plans_system;

-- +goose Down
DROP TABLE customer_block_appeals;
