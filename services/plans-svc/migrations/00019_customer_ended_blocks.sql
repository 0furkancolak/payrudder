-- Plan 13: the sweep that clears a block that ran out by itself lists the customers whose blocked_until has passed. Only blocked customers with an end are in the index,
-- so the list stays cheap however many customers there are.

-- +goose Up
CREATE INDEX customers_block_ends ON customers (blocked_until) WHERE blocked_at IS NOT NULL AND blocked_until IS NOT NULL;

-- +goose Down
DROP INDEX customers_block_ends;
