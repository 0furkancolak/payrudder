-- Phase 15: subscriptions a provider runs for us (Paddle), as the provider's notifications report them.
-- +goose Up
CREATE TABLE provider_subscriptions (
  tenant_id              uuid NOT NULL,
  provider               text NOT NULL,
  environment            text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  provider_ref           text NOT NULL,
  quotify_subscription_id uuid,                 -- the subscription this one was created for, when the provider says
  status                 text NOT NULL,         -- the provider's own word: active, trialing, past_due, paused, canceled
  next_billed_at         timestamptz,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, provider, environment, provider_ref)
);
CREATE INDEX provider_subscriptions_quotify ON provider_subscriptions (tenant_id, quotify_subscription_id) WHERE quotify_subscription_id IS NOT NULL;

GRANT SELECT, INSERT, UPDATE ON provider_subscriptions TO payments_system;
GRANT SELECT ON provider_subscriptions TO payments_app;

-- +goose Down
DROP TABLE provider_subscriptions;
