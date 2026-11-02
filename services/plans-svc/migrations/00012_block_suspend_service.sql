-- Plan 13: a block may also suspend the customer's service: limits-svc answers "not allowed" on every meter while it holds (the customer's suspension record on
-- plans.entitlements.v1 is written in the same transaction as the block). The history keeps what each block asked for.

-- +goose Up
ALTER TABLE customers
  ADD COLUMN block_suspend_service boolean NOT NULL DEFAULT false,
  ADD CONSTRAINT customers_suspend_needs_block CHECK (blocked_at IS NOT NULL OR NOT block_suspend_service);
ALTER TABLE customer_block_events ADD COLUMN suspend_service boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE customer_block_events DROP COLUMN suspend_service;
ALTER TABLE customers DROP CONSTRAINT customers_suspend_needs_block, DROP COLUMN block_suspend_service;
