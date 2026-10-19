-- Phase 15: a subscription can be sold and run by a payment provider (Paddle). The provider is chosen when it is created (phase 16 chooses it by
-- country and currency); who owns renewal and dunning follows from the provider's capabilities; the provider's id of the subscription is
-- linked when the provider tells us it exists.

-- +goose Up
ALTER TABLE subscriptions
  ADD COLUMN provider           text CHECK (provider ~ '^[a-z][a-z0-9_-]{0,31}$'),
  ADD COLUMN provider_ref       text,
  ADD COLUMN owner              text NOT NULL DEFAULT 'quotify' CHECK (owner IN ('quotify', 'provider')),
  ADD COLUMN provider_synced_at timestamptz;
-- A provider-owned subscription has a provider and its id there.
ALTER TABLE subscriptions ADD CONSTRAINT provider_owned_has_provider CHECK (owner = 'quotify' OR (provider IS NOT NULL AND provider_ref IS NOT NULL));
CREATE UNIQUE INDEX subscription_provider_ref ON subscriptions (tenant_id, provider, provider_ref) WHERE provider_ref IS NOT NULL;

-- +goose Down
DROP INDEX subscription_provider_ref;
ALTER TABLE subscriptions DROP CONSTRAINT provider_owned_has_provider;
ALTER TABLE subscriptions DROP COLUMN provider_synced_at, DROP COLUMN owner, DROP COLUMN provider_ref, DROP COLUMN provider;
