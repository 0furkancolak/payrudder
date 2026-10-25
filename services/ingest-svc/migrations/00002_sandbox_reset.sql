-- Phase 45 E: resetting a sandbox deletes the tenant's dedup keys.

-- +goose Up
GRANT SELECT, DELETE ON idempotency_keys TO ingest_system;

-- +goose Down
REVOKE SELECT, DELETE ON idempotency_keys FROM ingest_system;
