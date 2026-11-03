-- Plan 20: a key's own rate limit, as a share of the tenant's limit for each route class. NULL = no limit of its own (the key only draws on the tenant's budget).
-- A percentage rather than a number follows the tenant's tier when it changes. api_keys has no tenant twin: it lives only in the pooled database (a key is found by
-- its prefix before the tenant is known).

-- +goose Up
ALTER TABLE api_keys ADD COLUMN rate_percent integer CHECK (rate_percent BETWEEN 1 AND 100);

-- +goose Down
ALTER TABLE api_keys DROP COLUMN rate_percent;
