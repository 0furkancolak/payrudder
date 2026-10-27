-- Phase 28 stage 1: the tenant directory (ADR 0018). A tenant row says where the tenant's data lives: cell '' is the pooled database (row-level security, today's mode);
-- any other value names a cell, a Postgres cluster with one schema per tenant. Nothing reads these columns to decide anything yet: every row stays pooled.
-- region is where the cell is (informational, for the operator); placement_status is the fence of a move.

-- +goose Up
ALTER TABLE tenants
  ADD COLUMN cell text NOT NULL DEFAULT '' CHECK (cell = '' OR cell ~ '^[a-z0-9][a-z0-9-]{0,30}$'),
  ADD COLUMN region text NOT NULL DEFAULT '' CHECK (region = '' OR region ~ '^[a-z0-9][a-z0-9-]{0,30}$'),
  ADD COLUMN placement_status text NOT NULL DEFAULT 'ready' CHECK (placement_status IN ('ready', 'provisioning', 'moving'));
-- Cells hold few tenants; the operator lists them by cell.
CREATE INDEX tenants_cell ON tenants (cell) WHERE cell <> '';

-- +goose Down
DROP INDEX tenants_cell;
ALTER TABLE tenants DROP COLUMN placement_status, DROP COLUMN region, DROP COLUMN cell;
