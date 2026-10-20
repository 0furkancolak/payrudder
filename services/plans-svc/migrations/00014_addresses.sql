-- Phase 25: postal addresses. A customer's billing address and a tenant's (seller) address are printed on the invoice PDF and the hosted invoice page.
-- Stored as one JSON object ({line1, line2, city, region, postal_code, country}); nothing queries inside it. The customer's country stays its own column,
-- which routing and tax read, and is kept equal to the address's country.

-- +goose Up
ALTER TABLE customers ADD COLUMN address jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(address) = 'object');
ALTER TABLE tenants ADD COLUMN address jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(address) = 'object');
GRANT UPDATE (address) ON customers TO plans_app;

-- +goose Down
ALTER TABLE tenants DROP COLUMN address;
ALTER TABLE customers DROP COLUMN address;
