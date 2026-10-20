-- Phase 23: outbound events. A tenant registers HTTPS endpoints; Quotify records an event for what happens to its subscriptions, invoices and credit notes
-- and delivers it, signed, to every active endpoint that wants that type, retrying with a backoff. An event has one id however often it is delivered.

-- +goose Up
CREATE TABLE webhook_endpoints (
  tenant_id         uuid NOT NULL,
  id                uuid NOT NULL,
  url               text NOT NULL CHECK (length(url) <= 2000),
  description       text NOT NULL DEFAULT '' CHECK (length(description) <= 200),
  event_types       text[] NOT NULL DEFAULT '{}',          -- empty: every type
  secret_enc        bytea NOT NULL,                          -- sealed with QUOTIFY_SECRETS_KEY
  prev_secret_enc   bytea,                                   -- still signed with, until prev_secret_until
  prev_secret_until timestamptz,
  active            boolean NOT NULL DEFAULT true,
  created_at        timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id)
);

CREATE TABLE webhook_events (
  tenant_id   uuid NOT NULL,
  id          uuid NOT NULL,
  type        text NOT NULL,
  data        jsonb NOT NULL,
  occurred_at timestamptz NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id)
);

CREATE TABLE webhook_deliveries (
  tenant_id       uuid NOT NULL,
  id              uuid NOT NULL,
  event_id        uuid NOT NULL,
  endpoint_id     uuid NOT NULL,
  state           text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'delivered', 'failed')),
  attempts        int NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),      -- while pending: when the dispatcher may try again (also its lease)
  last_status     int,
  last_error      text,
  last_attempt_at timestamptz,
  delivered_at    timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, event_id, endpoint_id),
  FOREIGN KEY (tenant_id, event_id) REFERENCES webhook_events (tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, endpoint_id) REFERENCES webhook_endpoints (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX webhook_deliveries_due ON webhook_deliveries (next_attempt_at) WHERE state = 'pending';
CREATE INDEX webhook_deliveries_by_endpoint ON webhook_deliveries (tenant_id, endpoint_id, created_at DESC);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['webhook_endpoints', 'webhook_events', 'webhook_deliveries'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd
GRANT SELECT, INSERT, UPDATE, DELETE ON webhook_endpoints, webhook_deliveries TO plans_app;
GRANT SELECT, INSERT ON webhook_events TO plans_app;
-- The dispatcher runs across tenants.
GRANT SELECT ON webhook_endpoints TO plans_system;
GRANT SELECT, DELETE ON webhook_events TO plans_system;
GRANT SELECT, UPDATE, DELETE ON webhook_deliveries TO plans_system;

-- +goose Down
DROP TABLE webhook_deliveries, webhook_events, webhook_endpoints;
