-- payments_db (docs/05-data-model.md#payments_db): payments-worker's own state. It holds no money logic and no tenant
-- data beyond ids, so it has no RLS: only payments_system touches it.
-- +goose Up
CREATE TABLE payment_attempts (
  command_id       uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL,
  invoice_id       uuid NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('charge', 'refund', 'status')),
  environment      text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  provider         text NOT NULL,
  idempotency_key  text NOT NULL,
  request          jsonb NOT NULL,
  reply            bytea,                       -- the stored ReplyEnvelope; NULL while STARTED
  status           text NOT NULL CHECK (status IN ('STARTED', 'DONE')),
  created_at       timestamptz NOT NULL DEFAULT now(),
  finished_at      timestamptz
);
CREATE INDEX payment_attempts_invoice ON payment_attempts (invoice_id, kind);

GRANT SELECT, INSERT, UPDATE ON payment_attempts TO payments_system;
GRANT SELECT ON payment_attempts TO payments_app;

-- +goose Down
DROP TABLE payment_attempts;
