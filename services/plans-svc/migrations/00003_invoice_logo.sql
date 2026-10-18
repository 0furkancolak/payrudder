-- Phase 07: the tenant's invoice logo is an assets-svc asset (invoice_logo purpose); the id is a plain reference, since
-- assets live in another service's database.

-- +goose Up
ALTER TABLE tenants ADD COLUMN invoice_logo_asset_id uuid;

-- +goose Down
ALTER TABLE tenants DROP COLUMN invoice_logo_asset_id;
