-- Reconciliation as a workflow (docs/plans/22 G7): the discrepancies a comparison of billing with the payment ledger finds are kept, with who owns them and how they
-- ended; what the provider says it settled (balance transactions, a payout report, a CSV) is kept beside the ledger, so "paid" and "settled" are different states.
-- Like the other tables of payments_db these hold ids, amounts and notes only and have no row-level security (only payments_system touches them).
-- +goose Up

-- One row per thing that does not agree, per tenant and billing period. fingerprint is a hash of kind, invoice, provider and provider reference, so a run that finds the
-- same thing again finds this row. Identity columns never change; the status, owner and note do (a run that sees it again, an operator who explains or resolves it).
CREATE TABLE reconciliation_discrepancies (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id       uuid NOT NULL,
  environment     text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  period          text NOT NULL CHECK (period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
  fingerprint     text NOT NULL,
  kind            text NOT NULL,
  invoice_id      uuid,
  invoice_number  text NOT NULL DEFAULT '',
  provider        text NOT NULL DEFAULT '',
  provider_ref    text NOT NULL DEFAULT '',
  currency        text NOT NULL DEFAULT '',
  billed_minor    bigint NOT NULL DEFAULT 0,
  ledger_minor    bigint NOT NULL DEFAULT 0,
  settled_minor   bigint NOT NULL DEFAULT 0,           -- what the provider reported, for the settlement kinds (the fee, for a fee that is not the expected one)
  detail          text NOT NULL DEFAULT '',
  status          text NOT NULL CHECK (status IN ('open', 'explained', 'resolved')),
  owner           text NOT NULL DEFAULT '',
  note            text NOT NULL DEFAULT '',
  resolution      text NOT NULL DEFAULT '',            -- how it ended, once resolved ('cleared' = a later run no longer finds it)
  first_seen_at   timestamptz NOT NULL DEFAULT now(),
  last_seen_at    timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  resolved_at     timestamptz,
  UNIQUE (tenant_id, period, fingerprint)
);
CREATE INDEX reconciliation_discrepancies_queue ON reconciliation_discrepancies (tenant_id, status, id DESC);
CREATE INDEX reconciliation_discrepancies_invoice ON reconciliation_discrepancies (tenant_id, invoice_id) WHERE invoice_id IS NOT NULL;

-- What happened to a discrepancy, in order: found, reopened, cleared by a later run, assigned, explained, resolved. Append-only (no UPDATE is granted, and a delete is
-- refused as for the ledger): a correction is a new row. actor is "system" for a run, otherwise who the gateway says.
CREATE TABLE reconciliation_events (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id       uuid NOT NULL,
  environment     text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  discrepancy_id  bigint NOT NULL REFERENCES reconciliation_discrepancies (id),
  action          text NOT NULL CHECK (action IN ('detected', 'reopened', 'cleared', 'assigned', 'explained', 'resolved')),
  actor           text NOT NULL,
  from_status     text NOT NULL DEFAULT '',
  to_status       text NOT NULL DEFAULT '',
  resolution      text NOT NULL DEFAULT '',
  note            text NOT NULL DEFAULT '',
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX reconciliation_events_by_discrepancy ON reconciliation_events (discrepancy_id, id);

-- One row per run, appended when it ends: when, for which period, started by whom, what it saw and what it changed. A failed run keeps its reason.
CREATE TABLE reconciliation_runs (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id       uuid NOT NULL,
  environment     text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  period          text NOT NULL,
  started_by      text NOT NULL CHECK (started_by IN ('schedule', 'operator')),
  actor           text NOT NULL,
  result          text NOT NULL CHECK (result IN ('ok', 'truncated', 'failed')),  -- truncated: more was found than a run keeps, so nothing was cleared
  paid_invoices   bigint NOT NULL DEFAULT 0,
  charges         bigint NOT NULL DEFAULT 0,
  found           bigint NOT NULL DEFAULT 0,
  opened          bigint NOT NULL DEFAULT 0,
  reopened        bigint NOT NULL DEFAULT 0,
  cleared         bigint NOT NULL DEFAULT 0,
  error           text NOT NULL DEFAULT '',
  finished_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX reconciliation_runs_by_tenant ON reconciliation_runs (tenant_id, id DESC);

-- What the provider (or the bank) says it settled for a payment, a payout or a batch. gross, fee and net are signed minor units in currency (the settlement currency,
-- which can differ from the charge's); payment_ref is the provider_ref of the ledger charge the line is about, empty for a payout. available_on is when the funds
-- become available, settled_at when they were paid out, batch the payout or settlement reference. A later read of the same line (line_ref) fills in what was unknown.
CREATE TABLE settlement_lines (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id     uuid NOT NULL,
  environment   text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  provider      text NOT NULL,
  line_ref      text NOT NULL,
  kind          text NOT NULL CHECK (kind IN ('payment', 'payout')),
  payment_ref   text NOT NULL DEFAULT '',
  currency      text NOT NULL,
  gross_minor   bigint NOT NULL,
  fee_minor     bigint NOT NULL DEFAULT 0,
  net_minor     bigint NOT NULL,
  occurred_at   timestamptz NOT NULL,
  available_on  timestamptz,
  settled_at    timestamptz,
  batch         text NOT NULL DEFAULT '',
  source        text NOT NULL CHECK (source IN ('stripe', 'csv')),
  imported_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, provider, line_ref)
);
CREATE INDEX settlement_lines_by_payment ON settlement_lines (tenant_id, provider, payment_ref) WHERE payment_ref <> '';

-- What a provider is expected to charge the tenant per payment (the fee schedule of the tenant's contract, entered by an operator: Payrudder knows no provider's
-- prices). expected fee = gross * rate_bps / 10000, rounded half up, + fixed_minor, for lines in currency.
CREATE TABLE fee_rules (
  tenant_id    uuid NOT NULL,
  environment  text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  provider     text NOT NULL,
  currency     text NOT NULL,
  rate_bps     integer NOT NULL CHECK (rate_bps BETWEEN 0 AND 10000),
  fixed_minor  bigint NOT NULL DEFAULT 0 CHECK (fixed_minor >= 0),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, environment, provider, currency)
);

-- The events refuse a delete the way the ledger does (migration 00002): only a transaction that names the tenant as being reset (a sandbox row) or moved.
-- +goose StatementBegin
CREATE FUNCTION reconciliation_events_no_delete() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF current_setting('payrudder.tenant_move', true) = OLD.tenant_id::text THEN
    RETURN OLD;
  END IF;
  IF current_setting('payrudder.sandbox_reset', true) = OLD.tenant_id::text AND OLD.environment = 'sandbox' THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'rows of % are never deleted: a correction is a new row', TG_TABLE_NAME USING ERRCODE = 'P0001', HINT = 'ledger_row_delete';
END $$;
-- +goose StatementEnd
CREATE TRIGGER reconciliation_events_no_delete BEFORE DELETE ON reconciliation_events FOR EACH ROW EXECUTE FUNCTION reconciliation_events_no_delete();
CREATE TRIGGER reconciliation_runs_no_delete BEFORE DELETE ON reconciliation_runs FOR EACH ROW EXECUTE FUNCTION reconciliation_events_no_delete();

GRANT SELECT, INSERT ON reconciliation_discrepancies, reconciliation_events, reconciliation_runs, settlement_lines, fee_rules TO payments_system;
GRANT UPDATE (invoice_number, currency, billed_minor, ledger_minor, settled_minor, detail, status, owner, note, resolution, last_seen_at, updated_at, resolved_at) ON reconciliation_discrepancies TO payments_system;
GRANT UPDATE (currency, gross_minor, fee_minor, net_minor, available_on, settled_at, batch) ON settlement_lines TO payments_system;
GRANT UPDATE (rate_bps, fixed_minor, updated_at) ON fee_rules TO payments_system;
-- Resetting a sandbox deletes a tenant's rows of these as well; the triggers above refuse it for anything but the sandbox rows of a tenant being reset.
GRANT DELETE ON reconciliation_discrepancies, reconciliation_events, reconciliation_runs, settlement_lines, fee_rules TO payments_system;
GRANT SELECT ON reconciliation_discrepancies, reconciliation_events, reconciliation_runs, settlement_lines, fee_rules TO payments_app;

-- +goose Down
DROP TABLE fee_rules, settlement_lines, reconciliation_runs, reconciliation_events, reconciliation_discrepancies;
DROP FUNCTION reconciliation_events_no_delete();
