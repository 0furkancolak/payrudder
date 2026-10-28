-- A customer the seller has blocked: no new subscription; what already exists runs on until the seller ends it. blocked_at is when, block_reason
-- is the seller's own note (never shown to the customer).

-- +goose Up
ALTER TABLE customers
  ADD COLUMN blocked_at timestamptz,
  ADD COLUMN block_reason text NOT NULL DEFAULT '' CHECK (length(block_reason) <= 500),
  ADD CONSTRAINT customers_reason_needs_block CHECK (blocked_at IS NOT NULL OR block_reason = '');

-- +goose Down
ALTER TABLE customers DROP CONSTRAINT customers_reason_needs_block, DROP COLUMN block_reason, DROP COLUMN blocked_at;
