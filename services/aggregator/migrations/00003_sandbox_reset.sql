-- Phase 45 E: resetting a sandbox deletes the tenant's period settings too (usage, late usage and processed events can already be deleted: a replay does).

-- +goose Up
GRANT DELETE ON period_settings TO aggregator_system;

-- +goose Down
REVOKE DELETE ON period_settings FROM aggregator_system;
