-- Phase 45 follow-up: who connected, replaced or removed a tenant's provider account, and whether the provider was asked to vouch for the credentials. Append-only:
-- a correction is a new row. No secret is ever written here; the settings stay sealed in provider_accounts.

-- +goose Up
CREATE TABLE provider_account_events (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id        uuid NOT NULL,
  provider         text NOT NULL,
  environment      text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  action           text NOT NULL CHECK (action IN ('connected', 'replaced', 'removed')),
  actor            text NOT NULL DEFAULT '',        -- "user:<uuid>" for a person in the console, "operator" for support, '' when nobody was named
  is_default       boolean NOT NULL DEFAULT false,
  verified_remote  boolean NOT NULL DEFAULT false,  -- the provider itself accepted the credentials when they were stored
  created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX provider_account_events_by_tenant ON provider_account_events (tenant_id, created_at DESC);

GRANT SELECT, INSERT ON provider_account_events TO payments_system;
GRANT SELECT ON provider_account_events TO payments_app;

-- +goose Down
DROP TABLE provider_account_events;
