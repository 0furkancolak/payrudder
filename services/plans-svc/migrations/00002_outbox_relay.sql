-- Phase 04: plans-svc relays plans.entitlements.v1 through its outbox (pkg/outbox expects ingested_at).

-- +goose Up
ALTER TABLE outbox RENAME COLUMN created_at TO ingested_at;

-- FOR UPDATE SKIP LOCKED needs an UPDATE privilege on at least one column; id is GENERATED ALWAYS, so it is never
-- actually changed.
GRANT UPDATE (id) ON outbox TO plans_system;

-- +goose Down
REVOKE UPDATE (id) ON outbox FROM plans_system;
ALTER TABLE outbox RENAME COLUMN ingested_at TO created_at;
