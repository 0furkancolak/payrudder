-- billing_db schema (docs/05-data-model.md#billing_db).
-- Roles: billing_migrator owns everything; billing_app (read APIs, RLS enforced); billing_system (scheduler, sweeper,
-- reply consumer, relay and the admin/sandbox RPCs: BYPASSRLS).

-- +goose Up
CREATE FUNCTION app_tenant_id() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE
AS $$ SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid $$;

CREATE TABLE billing_runs (
  tenant_id         uuid NOT NULL,
  period_id         text NOT NULL CHECK (period_id ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
  status            text NOT NULL CHECK (status IN ('SCHEDULED','WAITING_WATERMARK','RATING','INVOICING','COMPLETED','FAILED')),
  cutoff_at         timestamptz NOT NULL,
  generation        int,                               -- aggregate generation used; pinned at RATING
  required_offsets  jsonb,
  invoice_count     int NOT NULL DEFAULT 0,
  last_error        text,
  created_at        timestamptz NOT NULL DEFAULT now(),
  started_at        timestamptz,
  completed_at      timestamptz,
  PRIMARY KEY (tenant_id, period_id)
);
CREATE INDEX billing_runs_open ON billing_runs (cutoff_at) WHERE status <> 'COMPLETED';

CREATE TABLE invoices (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  customer_id      uuid NOT NULL,
  period_id        text NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('REGULAR','CORRECTION')),
  number           text,                               -- 'INV-2026-000123', set at FINALIZED
  plan_version_id  uuid NOT NULL,
  currency         char(3) NOT NULL,
  subtotal_minor   bigint NOT NULL,
  total_minor      bigint NOT NULL CHECK (total_minor >= 0),
  status           text NOT NULL CHECK (status IN ('draft','open','paid','uncollectible','void')),
  version          int NOT NULL DEFAULT 1,
  pdf_object_key   text,
  -- Everything the saga's commands need about the customer and the tenant, frozen when the draft is created:
  -- {customer_external_id, customer_name, customer_email, payment_method, tenant_name, environment, currency_exponent}.
  snapshot         jsonb NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  finalized_at     timestamptz,
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, number)
);
CREATE UNIQUE INDEX one_regular_invoice ON invoices (tenant_id, customer_id, period_id) WHERE kind = 'REGULAR';
CREATE INDEX invoices_by_customer ON invoices (tenant_id, customer_id, period_id);

CREATE TABLE invoice_lines (
  tenant_id        uuid NOT NULL,
  invoice_id       uuid NOT NULL,
  line_no          int  NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('BASE_FEE','USAGE','LATE_USAGE','CREDIT','CORRECTION')),
  meter_code       text,
  origin_period_id text,                               -- for LATE_USAGE
  quantity         numeric(38,0),                      -- scaled units; displayed with the meter's scale + unit_label
  quantity_scale   smallint,
  unit_label       text,
  unit_price       numeric(30,12),                     -- display only for tiered
  amount_minor     bigint NOT NULL,                    -- may be negative for CREDIT
  description      text NOT NULL,
  PRIMARY KEY (tenant_id, invoice_id, line_no),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);
-- Late usage already billed for an origin period, summed to price the next late batch incrementally.
CREATE INDEX invoice_lines_late ON invoice_lines (tenant_id, meter_code, origin_period_id) WHERE kind = 'LATE_USAGE';

CREATE TABLE invoice_number_seq (
  tenant_id uuid NOT NULL, year int NOT NULL, next_value bigint NOT NULL CHECK (next_value >= 1),
  PRIMARY KEY (tenant_id, year)
);  -- SELECT ... FOR UPDATE at finalize → gap-free per tenant/year

CREATE TABLE invoice_sagas (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,                      -- saga_id
  invoice_id       uuid NOT NULL,
  state            text NOT NULL,                      -- see internal/saga
  version          int  NOT NULL,                      -- optimistic concurrency
  attempt          int  NOT NULL DEFAULT 0,            -- commands issued, monotonic: part of every command id
  step_attempt     int  NOT NULL DEFAULT 0,            -- commands issued in the current step (cap 3), reset on entering a step
  step_deadline    timestamptz,
  dunning_attempt  int  NOT NULL DEFAULT 0,
  next_attempt_at  timestamptz,
  last_error       text,
  updated_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, invoice_id),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);
CREATE INDEX saga_deadlines ON invoice_sagas (step_deadline) WHERE step_deadline IS NOT NULL;
CREATE INDEX saga_dunning   ON invoice_sagas (next_attempt_at) WHERE state = 'PAYMENT_DECLINED';
CREATE INDEX saga_state     ON invoice_sagas (tenant_id, state);

CREATE TABLE saga_events (                              -- append-only audit log
  tenant_id uuid NOT NULL, saga_id uuid NOT NULL, seq int NOT NULL,
  from_state text NOT NULL, to_state text NOT NULL, cause text NOT NULL,   -- e.g. 'reply:PaymentDeclined'
  command_id uuid, at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, saga_id, seq)
);

CREATE TABLE inbox_replies (message_id uuid PRIMARY KEY, received_at timestamptz NOT NULL DEFAULT now());

CREATE TABLE credit_balances (
  tenant_id uuid NOT NULL, customer_id uuid NOT NULL, id uuid NOT NULL,
  amount_minor bigint NOT NULL CHECK (amount_minor > 0), source_rerating_job_id uuid NOT NULL,
  applied_invoice_id uuid,                              -- NULL = unapplied
  PRIMARY KEY (tenant_id, id)
);

CREATE TABLE rerating_jobs (
  tenant_id uuid NOT NULL, id uuid NOT NULL, period_id text NOT NULL, plan_version_id uuid NOT NULL,
  generation int NOT NULL, status text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, period_id, plan_version_id)       -- idempotent re-rating
);

-- The shared outbox shape of pkg/outbox (id, tenant_id, topic, partition_key, payload, headers, ingested_at):
-- topic is the exchange or Kafka topic, partition_key the routing or record key, and the header x-destination
-- picks the sink ("rabbitmq" | "kafka"). message_id makes a re-issued command a no-op.
CREATE TABLE outbox (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id      uuid NOT NULL,
  topic          text NOT NULL,
  partition_key  text NOT NULL,
  payload        bytea,
  headers        jsonb NOT NULL DEFAULT '{}',
  message_id     uuid NOT NULL UNIQUE,
  ingested_at    timestamptz NOT NULL DEFAULT now()
);

-- Row level security on every tenant-owned table the app role can read.
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['billing_runs', 'invoices', 'invoice_lines', 'invoice_sagas', 'saga_events', 'credit_balances', 'rerating_jobs'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT ON billing_runs, invoices, invoice_lines, invoice_sagas TO billing_app;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO billing_app;

GRANT SELECT, INSERT, UPDATE ON billing_runs, invoices, invoice_sagas, invoice_number_seq, credit_balances, rerating_jobs TO billing_system;
GRANT SELECT, INSERT ON invoice_lines, saga_events, inbox_replies TO billing_system;
GRANT DELETE ON invoice_lines TO billing_system;  -- a draft's lines are replaced when its run is retried
GRANT SELECT, INSERT ON outbox TO billing_system;
-- The relay claims with FOR UPDATE SKIP LOCKED, which needs an UPDATE privilege on a column.
GRANT DELETE, UPDATE (id) ON outbox TO billing_system;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO billing_system;

-- +goose Down
DROP TABLE outbox, rerating_jobs, credit_balances, inbox_replies, saga_events, invoice_sagas, invoice_number_seq,
  invoice_lines, invoices, billing_runs;
DROP FUNCTION app_tenant_id();
