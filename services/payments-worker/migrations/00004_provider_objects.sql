-- Phase 21: what Quotify has created at a provider (products, prices, customers, discounts), so it is created once and found again.
-- +goose Up
CREATE TABLE provider_objects (
  tenant_id   uuid NOT NULL,
  provider    text NOT NULL,
  environment text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  kind        text NOT NULL CHECK (kind IN ('product', 'price', 'customer', 'address', 'business', 'discount')),
  key         text NOT NULL,   -- Quotify's name for it: plan:{code}, {plan version id}|{interval}|{currency}, cus:{customer id}, ...
  ref         text NOT NULL,   -- the provider's id
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, provider, environment, kind, key)
);
CREATE INDEX provider_objects_by_ref ON provider_objects (tenant_id, provider, environment, kind, ref);

GRANT SELECT, INSERT ON provider_objects TO payments_system;
GRANT SELECT ON provider_objects TO payments_app;

-- +goose Down
DROP TABLE provider_objects;
