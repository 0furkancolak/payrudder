-- billing_db, tenant schema (phase 28, ADR 0018): what one tenant of a cell owns, in its own schema t_<id>.
--
-- It is the sum of ../00001_init.sql to ../00055_*.sql written for a schema instead of public: the same tables, columns, constraints, indexes, policies and privileges
-- as the public migrations leave behind, but no name of a schema anywhere and none of the data fixes the history needed. They run with the search path set to that
-- schema; each schema keeps its own goose_db_version. A change to ../ is a change here too: TestTenantSchemaMatchesPublic compares the two.
-- Roles are the service's own (billing_app, billing_system); the cell database has the same ones.
--
-- Every table is here: billing-svc has none that is not a tenant's. inbox_replies has no tenant_id (it is keyed by the id of the reply message, which is unique on its
-- own), so a move between the pooled database and a cell leaves it where it is: the rows only stop a redelivered reply from being applied twice, and the saga ignores
-- a reply that is stale for its state anyway.
--
-- Constraints that were renamed or re-added by the public history keep their final public names.

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
  kind             text NOT NULL CONSTRAINT invoices_kind_check CHECK (kind IN ('REGULAR','CORRECTION','FEE','LINK')),
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
  rerating_job_id  uuid,
  provider         text NOT NULL DEFAULT '',
  country          char(2),
  fee_key          text,                               -- '{subscription id}|{period start, RFC 3339 UTC}' for kind FEE
  coupon_code      text,
  promotion_code   text,
  promotion_target text CHECK (promotion_target IN ('credit_purchase', 'payment')),
  bonus_credits    bigint NOT NULL DEFAULT 0 CHECK (bonus_credits >= 0),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, number),
  CONSTRAINT invoice_promotion_shape CHECK ((coupon_code IS NULL) = (promotion_target IS NULL)),
  -- A sandbox tenant's invoices are numbered SBX-2026-000123; a live tenant's never start with SBX-.
  CONSTRAINT invoice_number_series CHECK (
    coalesce(snapshot ->> 'environment' IN ('sandbox', 'live'), false)
    AND (number IS NULL OR (number LIKE 'SBX-%') = (snapshot ->> 'environment' = 'sandbox')))
);
CREATE UNIQUE INDEX one_regular_invoice ON invoices (tenant_id, customer_id, period_id) WHERE kind = 'REGULAR';
CREATE INDEX invoices_by_customer ON invoices (tenant_id, customer_id, period_id);
CREATE UNIQUE INDEX one_correction_per_job ON invoices (tenant_id, customer_id, rerating_job_id) WHERE rerating_job_id IS NOT NULL;
CREATE INDEX invoices_report ON invoices (tenant_id, period_id, currency);
CREATE UNIQUE INDEX one_fee_invoice ON invoices (tenant_id, fee_key) WHERE fee_key IS NOT NULL;
CREATE INDEX invoices_open ON invoices (tenant_id, finalized_at) WHERE status = 'open';

CREATE TABLE invoice_lines (
  tenant_id        uuid NOT NULL,
  invoice_id       uuid NOT NULL,
  line_no          int  NOT NULL,
  kind             text NOT NULL CONSTRAINT invoice_lines_kind_check CHECK (kind IN ('BASE_FEE','USAGE','LATE_USAGE','CREDIT','CORRECTION','DISCOUNT','TAX')),
  meter_code       text,
  origin_period_id text,                               -- for LATE_USAGE
  quantity         numeric(38,0),                      -- scaled units; displayed with the meter's scale + unit_label
  quantity_scale   smallint,
  unit_label       text,
  unit_price       numeric(30,12),                     -- display only for tiered
  amount_minor     bigint NOT NULL,                    -- may be negative for CREDIT
  description      text NOT NULL,
  service_from     timestamptz,
  service_to       timestamptz,
  coupon_code      text,
  PRIMARY KEY (tenant_id, invoice_id, line_no),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);
-- Late usage already billed for an origin period, summed to price the next late batch incrementally.
CREATE INDEX invoice_lines_late ON invoice_lines (tenant_id, meter_code, origin_period_id) WHERE kind = 'LATE_USAGE';
CREATE INDEX invoice_lines_coupon ON invoice_lines (tenant_id, coupon_code, invoice_id) WHERE coupon_code IS NOT NULL;

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
-- Sagas that wait for a payment to settle (a bank transfer) are polled when next_attempt_at is due.
CREATE INDEX saga_polls ON invoice_sagas (next_attempt_at) WHERE state = 'PAYMENT_PENDING';

CREATE TABLE saga_events (                              -- append-only audit log
  tenant_id uuid NOT NULL, saga_id uuid NOT NULL, seq int NOT NULL,
  from_state text NOT NULL, to_state text NOT NULL, cause text NOT NULL,   -- e.g. 'reply:PaymentDeclined'
  command_id uuid, at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, saga_id, seq)
);
-- The income side reads the moment an invoice was paid or refunded from the saga history.
CREATE INDEX saga_events_paid ON saga_events (tenant_id, to_state, at) WHERE to_state IN ('PAID', 'REFUNDED');

CREATE TABLE inbox_replies (message_id uuid PRIMARY KEY, received_at timestamptz NOT NULL DEFAULT now());

CREATE TABLE rerating_jobs (
  tenant_id uuid NOT NULL, id uuid NOT NULL, period_id text NOT NULL, plan_version_id uuid NOT NULL,
  generation int NOT NULL, status text NOT NULL CONSTRAINT rerating_status CHECK (status IN ('RUNNING', 'COMPLETED', 'FAILED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  customers_rated    int    NOT NULL DEFAULT 0,
  corrections        int    NOT NULL DEFAULT 0,
  credits            int    NOT NULL DEFAULT 0,
  correction_minor   bigint NOT NULL DEFAULT 0,
  credit_minor       bigint NOT NULL DEFAULT 0,
  last_error         text,
  completed_at       timestamptz,
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, period_id, plan_version_id)       -- idempotent re-rating
);

CREATE TABLE credit_balances (
  tenant_id uuid NOT NULL, customer_id uuid NOT NULL, id uuid NOT NULL,
  amount_minor bigint NOT NULL CHECK (amount_minor > 0), source_rerating_job_id uuid,
  applied_invoice_id uuid,                              -- NULL = unapplied
  created_at timestamptz NOT NULL DEFAULT now(),
  source_adjustment_id uuid,
  currency char(3),
  source_credit_note_id uuid,
  source_invoice_id uuid,
  PRIMARY KEY (tenant_id, id),
  -- A credit balance comes from exactly one source: a re-rating job, a plan change's adjustment, a credit note, or an invoice that was voided after money was received.
  CONSTRAINT credit_has_one_source CHECK (num_nonnulls(source_rerating_job_id, source_adjustment_id, source_credit_note_id, source_invoice_id) = 1)
);
CREATE UNIQUE INDEX one_credit_per_job ON credit_balances (tenant_id, customer_id, source_rerating_job_id);
CREATE UNIQUE INDEX one_credit_per_adjustment ON credit_balances (tenant_id, source_adjustment_id) WHERE source_adjustment_id IS NOT NULL;
CREATE UNIQUE INDEX one_credit_per_credit_note ON credit_balances (tenant_id, source_credit_note_id) WHERE source_credit_note_id IS NOT NULL;
CREATE UNIQUE INDEX one_credit_per_voided_invoice ON credit_balances (tenant_id, source_invoice_id) WHERE source_invoice_id IS NOT NULL;

-- A credit can be consumed over several invoices (it is applied up to what the invoice costs); the ledger of those applications
-- is what the remainder is computed from. credit_balances.applied_invoice_id is set when the last of it is consumed.
CREATE TABLE credit_applications (
  tenant_id    uuid   NOT NULL,
  credit_id    uuid   NOT NULL,
  invoice_id   uuid   NOT NULL,
  amount_minor bigint NOT NULL CHECK (amount_minor > 0),
  PRIMARY KEY (tenant_id, credit_id, invoice_id),
  FOREIGN KEY (tenant_id, credit_id) REFERENCES credit_balances (tenant_id, id)
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

CREATE TABLE credit_note_seq (
  tenant_id uuid NOT NULL, year int NOT NULL, next_value bigint NOT NULL CHECK (next_value >= 1),
  PRIMARY KEY (tenant_id, year)
);

CREATE TABLE credit_notes (
  tenant_id    uuid NOT NULL,
  id           uuid NOT NULL,
  customer_id  uuid NOT NULL,
  invoice_id   uuid NOT NULL,
  number       text NOT NULL,                        -- 'CN-2026-000007', or 'SBX-CN-2026-000007' for a sandbox tenant
  currency     char(3) NOT NULL,
  total_minor  bigint NOT NULL CHECK (total_minor > 0),
  reason       text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL DEFAULT now(),
  disposition          text NOT NULL DEFAULT 'credit' CHECK (disposition IN ('credit', 'refund')),
  refund_state         text NOT NULL DEFAULT 'none' CHECK (refund_state IN ('none', 'requested', 'refunded', 'failed')),
  refund_attempt       int NOT NULL DEFAULT 0,
  refund_run_from      int NOT NULL DEFAULT 0,   -- the attempt a manual retry started from: a run is at most 3 attempts
  refund_generation    int NOT NULL DEFAULT 0,   -- part of the idempotency key; a retry after a rejection starts a new one
  refund_command_id    uuid,
  refund_requested_at  timestamptz,
  refund_ref           text,
  refund_error         text,
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, number),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id),
  CONSTRAINT refund_only_for_refunds CHECK (disposition = 'refund' OR refund_state = 'none')
);
CREATE INDEX credit_notes_by_invoice ON credit_notes (tenant_id, invoice_id);
CREATE INDEX credit_notes_by_customer ON credit_notes (tenant_id, customer_id, created_at DESC);
CREATE UNIQUE INDEX credit_notes_refund_command ON credit_notes (refund_command_id) WHERE refund_command_id IS NOT NULL;
CREATE INDEX credit_notes_refund_pending ON credit_notes (refund_requested_at) WHERE refund_state = 'requested';

-- A credit note takes the environment of its invoice, which a CHECK cannot see; a trigger reads it. The invoice is looked up in the schema of the table the trigger
-- fires on (a move copies rows into a schema with an empty search path), so the function needs no setting of its own.
-- +goose StatementBegin
CREATE FUNCTION credit_notes_check_series() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  env text;
BEGIN
  EXECUTE format('SELECT snapshot ->> %L FROM %I.invoices WHERE tenant_id = $1 AND id = $2', 'environment', TG_TABLE_SCHEMA) INTO env USING NEW.tenant_id, NEW.invoice_id;
  IF env IS NULL OR (NEW.number LIKE 'SBX-%') <> (env = 'sandbox') THEN
    RAISE EXCEPTION 'a credit note number must be in the series of its invoice''s environment' USING ERRCODE = 'P0001', HINT = 'number_series';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER credit_notes_series BEFORE INSERT OR UPDATE OF number ON credit_notes FOR EACH ROW EXECUTE FUNCTION credit_notes_check_series();

CREATE TABLE dunning_settings (
  tenant_id     uuid PRIMARY KEY,
  retry_hours   int[] NOT NULL CHECK (cardinality(retry_hours) <= 6),  -- delay before retry 1..n, in hours (minutes in a sandbox tenant)
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tenant_event_outbox (
  tenant_id   uuid NOT NULL,
  event_id    uuid NOT NULL,
  type        text NOT NULL,
  data        jsonb NOT NULL,
  occurred_at timestamptz NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, event_id)
);
CREATE INDEX tenant_event_outbox_by_age ON tenant_event_outbox (created_at);

-- Payment links. The public history called the table invoice_links; 00002 renames its constraints.
CREATE TABLE payment_links (
  tenant_id   uuid NOT NULL,
  id          uuid NOT NULL,
  invoice_id  uuid,                                   -- an ad-hoc link has no invoice until it is used
  token_hash  bytea NOT NULL CONSTRAINT invoice_links_token_hash_key UNIQUE CONSTRAINT invoice_links_token_hash_check CHECK (length(token_hash) = 32),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  revoked_at  timestamptz,
  kind                 text NOT NULL DEFAULT 'invoice' CHECK (kind IN ('invoice', 'adhoc', 'credit_pack')),
  parent_id            uuid,
  single_use           boolean NOT NULL DEFAULT false,
  idempotency_key      text,
  request_hash         bytea,
  amount_minor         bigint CHECK (amount_minor IS NULL OR amount_minor > 0),
  currency             char(3),
  description          text,
  customer_id          uuid,
  customer_external_id text,
  use_count            int NOT NULL DEFAULT 0,
  paid_count           int NOT NULL DEFAULT 0,
  last_paid_at         timestamptz,
  expired_notified_at  timestamptz,
  pack_id       uuid,
  quantity      int CHECK (quantity IS NULL OR quantity BETWEEN 1 AND 1000),
  credits       bigint CHECK (credits IS NULL OR credits > 0),       -- credits one use grants: the pack's credits times the quantity, frozen at creation
  validity_days int CHECK (validity_days IS NULL OR validity_days BETWEEN 1 AND 3650),
  promotion_code text CHECK (promotion_code IS NULL OR promotion_code ~ '^[A-Za-z0-9_-]{2,40}$'),
  CONSTRAINT invoice_links_pkey PRIMARY KEY (tenant_id, id),
  CONSTRAINT invoice_links_tenant_id_invoice_id_fkey FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id),
  CONSTRAINT payment_link_shape CHECK (
    (kind = 'invoice' AND invoice_id IS NOT NULL AND amount_minor IS NULL AND pack_id IS NULL)
    OR (kind = 'adhoc' AND invoice_id IS NULL AND amount_minor IS NOT NULL AND currency IS NOT NULL AND parent_id IS NULL AND pack_id IS NULL)
    OR (kind = 'credit_pack' AND invoice_id IS NULL AND amount_minor IS NOT NULL AND currency IS NOT NULL AND parent_id IS NULL AND pack_id IS NOT NULL
        AND quantity IS NOT NULL AND credits IS NOT NULL AND customer_id IS NOT NULL)),
  CONSTRAINT payment_link_parent FOREIGN KEY (tenant_id, parent_id) REFERENCES payment_links (tenant_id, id)
);
CREATE INDEX payment_links_by_invoice ON payment_links (tenant_id, invoice_id);
CREATE UNIQUE INDEX payment_links_idempotency ON payment_links (tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX payment_links_children ON payment_links (tenant_id, parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX payment_links_expiry ON payment_links (expires_at) WHERE parent_id IS NULL AND revoked_at IS NULL AND expired_notified_at IS NULL;
-- The open link a customer's portal already made for a pack, so that pressing "Buy" twice leads to one invoice.
CREATE INDEX payment_links_portal_purchase ON payment_links (tenant_id, customer_id, pack_id, quantity) WHERE kind = 'credit_pack' AND idempotency_key LIKE 'portal:%';

CREATE TABLE invoice_payments (
  tenant_id    uuid NOT NULL,
  id           uuid NOT NULL,
  invoice_id   uuid NOT NULL,
  method       text NOT NULL CHECK (method IN ('bank_transfer', 'cash', 'other')),
  amount_minor bigint NOT NULL CHECK (amount_minor > 0),
  currency     char(3) NOT NULL,
  received_at  timestamptz NOT NULL,
  reference    text NOT NULL DEFAULT '',   -- the bank's or the receipt's reference; with the method it makes the payment idempotent
  note         text NOT NULL DEFAULT '',
  recorded_by  text NOT NULL,
  recorded_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);
CREATE UNIQUE INDEX invoice_payment_once ON invoice_payments (tenant_id, invoice_id, method, reference) WHERE reference <> '';
CREATE INDEX invoice_payments_by_invoice ON invoice_payments (tenant_id, invoice_id, received_at);

CREATE TABLE portal_links (
  tenant_id   uuid NOT NULL,
  id          uuid NOT NULL,
  customer_id uuid NOT NULL,
  token_hash  bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  revoked_at  timestamptz,
  customer_external_id text,
  PRIMARY KEY (tenant_id, id)
);
CREATE INDEX portal_links_by_customer ON portal_links (tenant_id, customer_id);

CREATE TABLE credit_packs (
  tenant_id    uuid NOT NULL,
  id           uuid NOT NULL,
  name         text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  credits      bigint NOT NULL CHECK (credits > 0),
  price_minor  bigint NOT NULL CHECK (price_minor > 0),
  currency     char(3) NOT NULL,
  active       boolean NOT NULL DEFAULT true,
  created_at   timestamptz NOT NULL DEFAULT now(),
  validity_days int CHECK (validity_days IS NULL OR validity_days BETWEEN 1 AND 3650),   -- NULL: the credits never expire
  PRIMARY KEY (tenant_id, id)
);

CREATE TABLE credit_lots (
  tenant_id    uuid NOT NULL,
  id           uuid NOT NULL,
  customer_id  uuid NOT NULL,
  granted      bigint NOT NULL CHECK (granted > 0),
  remaining    bigint NOT NULL CHECK (remaining >= 0 AND remaining <= granted),
  source       text NOT NULL CHECK (source IN ('purchase', 'promo', 'manual')),
  source_ref   text,                       -- the payment link or invoice that paid for a purchase; one lot per reference
  expires_at   timestamptz,                -- NULL: never expires
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id)
);
CREATE UNIQUE INDEX one_lot_per_source_ref ON credit_lots (tenant_id, source, source_ref) WHERE source_ref IS NOT NULL;
-- The lots a consume walks, in the order it takes from them.
CREATE INDEX credit_lots_spendable ON credit_lots (tenant_id, customer_id, expires_at NULLS LAST, created_at) WHERE remaining > 0;

CREATE TABLE credit_ledger (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  customer_id      uuid NOT NULL,
  lot_id           uuid NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('grant', 'consume', 'expire')),
  amount           bigint NOT NULL CHECK (amount <> 0),   -- positive for a grant, negative for consume and expire
  idempotency_key  text,                                  -- a consume or grant the caller may repeat; the repeat finds the first
  reason           text NOT NULL DEFAULT '',
  created_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, lot_id) REFERENCES credit_lots (tenant_id, id),
  CHECK ((kind = 'grant') = (amount > 0))
);
CREATE INDEX credit_ledger_by_customer ON credit_ledger (tenant_id, customer_id, created_at DESC, id);
CREATE INDEX credit_ledger_by_key ON credit_ledger (tenant_id, customer_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TABLE credit_settings (
  tenant_id     uuid PRIMARY KEY,
  low_threshold bigint NOT NULL CHECK (low_threshold >= 0),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE einvoice_documents (
  tenant_id         uuid NOT NULL,
  id                uuid NOT NULL,
  kind              text NOT NULL CHECK (kind IN ('invoice', 'credit_note')),
  invoice_id        uuid NOT NULL,
  credit_note_id    uuid,
  number            text NOT NULL,
  environment       text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  status            text NOT NULL CHECK (status IN ('pending', 'issued', 'failed', 'cancelled')),
  document_type     text NOT NULL DEFAULT '',
  provider          text NOT NULL DEFAULT '',
  -- attempts only grows (it is part of the command id); streak counts the automatic attempts since the last operator action and drives the backoff.
  attempts          int NOT NULL DEFAULT 0,
  streak            int NOT NULL DEFAULT 0,
  -- pending: when the next attempt is due (NULL while a command is out; then last_dispatch_at + the reply wait applies)
  next_attempt_at   timestamptz,
  last_dispatch_at  timestamptz,
  cancel_requested  boolean NOT NULL DEFAULT false,
  last_error_key    text NOT NULL DEFAULT '',
  last_error        text NOT NULL DEFAULT '',
  legal_number      text,
  external_id       text NOT NULL DEFAULT '',
  xml_key           text,
  pdf_key           text,
  request           bytea NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  issued_at         timestamptz,
  updated_at        timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  CHECK ((kind = 'credit_note') = (credit_note_id IS NOT NULL)),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);
CREATE UNIQUE INDEX one_einvoice_per_invoice ON einvoice_documents (tenant_id, invoice_id) WHERE kind = 'invoice';
CREATE UNIQUE INDEX one_einvoice_per_credit_note ON einvoice_documents (tenant_id, credit_note_id) WHERE kind = 'credit_note';
-- A legal number is never reused: one document per number within a tenant and environment.
CREATE UNIQUE INDEX einvoice_legal_number_once ON einvoice_documents (tenant_id, environment, legal_number) WHERE legal_number IS NOT NULL;
CREATE INDEX einvoice_due ON einvoice_documents (status, next_attempt_at, last_dispatch_at) WHERE status = 'pending' OR cancel_requested;
CREATE INDEX einvoice_by_tenant ON einvoice_documents (tenant_id, created_at DESC, id DESC);

CREATE TABLE expenses (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  app_id           uuid,                       -- the app of the account the cost belongs to; NULL: the whole tenant
  vendor           text NOT NULL CHECK (length(vendor) BETWEEN 1 AND 120),
  category         text NOT NULL CHECK (category IN ('infrastructure', 'payment_fees', 'marketing', 'payroll', 'tax', 'software', 'other')),
  amount_minor     bigint NOT NULL CHECK (amount_minor > 0),
  currency         char(3) NOT NULL,
  incurred_on      date NOT NULL,              -- the first occurrence of a recurring expense
  note             text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  recurring        text NOT NULL DEFAULT 'none' CHECK (recurring IN ('none', 'monthly', 'yearly')),
  recurring_until  date,                       -- last day an occurrence may fall on; NULL: no end
  voided_at        timestamptz,
  void_reason      text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  tax_minor        bigint NOT NULL DEFAULT 0 CHECK (tax_minor >= 0),
  attachment_asset_id uuid,
  PRIMARY KEY (tenant_id, id),
  CHECK (recurring <> 'none' OR recurring_until IS NULL),
  CHECK (recurring_until IS NULL OR recurring_until >= incurred_on),
  CHECK ((voided_at IS NULL) = (void_reason IS NULL))
);
-- The summary and the list read a tenant's live expenses by date.
CREATE INDEX expenses_by_date ON expenses (tenant_id, incurred_on DESC, id) WHERE voided_at IS NULL;

CREATE TABLE promotion_reversals (
  tenant_id   uuid NOT NULL,
  invoice_id  uuid NOT NULL,
  target_kind text NOT NULL CHECK (target_kind IN ('credit_purchase', 'payment')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  attempts    int NOT NULL DEFAULT 0,
  done_at     timestamptz,
  PRIMARY KEY (tenant_id, invoice_id),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);
CREATE INDEX promotion_reversals_pending ON promotion_reversals (created_at) WHERE done_at IS NULL;

-- The invoice inbox: invoices+<token>@<inbound domain>. The token is the address (not a credential: mail to it only adds items to review), so it is kept as is.
CREATE TABLE expense_inboxes (
  tenant_id  uuid NOT NULL,
  scope      text NOT NULL DEFAULT '',
  token      text NOT NULL UNIQUE CHECK (length(token) BETWEEN 16 AND 64),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, scope)
);

CREATE TABLE expense_inbox_items (
  tenant_id    uuid NOT NULL,
  id           uuid NOT NULL,
  app_id       uuid,
  from_addr    text NOT NULL DEFAULT '' CHECK (length(from_addr) <= 320),
  subject      text NOT NULL DEFAULT '' CHECK (length(subject) <= 300),
  filename     text NOT NULL CHECK (length(filename) BETWEEN 1 AND 200),
  content_type text NOT NULL,
  size_bytes   bigint NOT NULL CHECK (size_bytes > 0),
  content      bytea NOT NULL,
  status       text NOT NULL DEFAULT 'to_review' CHECK (status IN ('to_review', 'accepted', 'dismissed')),
  expense_id   uuid,
  received_at  timestamptz NOT NULL DEFAULT now(),
  resolved_at  timestamptz,
  PRIMARY KEY (tenant_id, id)
);
CREATE INDEX expense_inbox_items_by_status ON expense_inbox_items (tenant_id, status, received_at DESC);

CREATE TABLE expense_rules (
  tenant_id             uuid NOT NULL,
  id                    uuid NOT NULL,
  app_id                uuid,
  vendor_match          text NOT NULL CHECK (length(vendor_match) BETWEEN 2 AND 120),
  category              text NOT NULL CHECK (category IN ('infrastructure', 'payment_fees', 'marketing', 'payroll', 'tax', 'software', 'other')),
  currency              char(3) NOT NULL,
  tax_bps               int NOT NULL DEFAULT 0 CHECK (tax_bps BETWEEN 0 AND 10000),
  expected_amount_minor bigint NOT NULL DEFAULT 0 CHECK (expected_amount_minor >= 0),
  tolerance_bps         int NOT NULL DEFAULT 0 CHECK (tolerance_bps BETWEEN 0 AND 10000),
  auto_approve          boolean NOT NULL DEFAULT false,
  created_at            timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  CHECK (NOT auto_approve OR expected_amount_minor > 0)
);

-- Row level security stays on in the tenant schema, as the second guard behind the search path, on every table the public schema has it on.
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['billing_runs', 'invoices', 'invoice_lines', 'invoice_sagas', 'saga_events', 'credit_balances', 'rerating_jobs', 'credit_applications',
                           'credit_notes', 'credit_note_seq', 'dunning_settings', 'tenant_event_outbox', 'payment_links', 'invoice_payments', 'portal_links', 'credit_packs',
                           'credit_lots', 'credit_ledger', 'credit_settings', 'einvoice_documents', 'expenses', 'promotion_reversals', 'expense_inbox_items', 'expense_rules'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- billing_app: the read APIs, and the few tables a tenant's console writes.
GRANT SELECT ON billing_runs, invoices, invoice_lines, invoice_sagas, saga_events, credit_balances, rerating_jobs, credit_applications, credit_notes, invoice_payments,
  promotion_reversals, einvoice_documents TO billing_app;
GRANT SELECT, INSERT, UPDATE ON dunning_settings, payment_links, portal_links, credit_packs, credit_lots, credit_settings, expenses, expense_inboxes, expense_inbox_items TO billing_app;
GRANT SELECT, INSERT ON credit_ledger TO billing_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON expense_rules TO billing_app;
GRANT INSERT ON tenant_event_outbox TO billing_app;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO billing_app;

-- billing_system: the scheduler, the sweeper, the reply consumer, the relay and the admin and sandbox RPCs (BYPASSRLS).
GRANT SELECT, INSERT, UPDATE, DELETE ON billing_runs, invoices, invoice_sagas, invoice_number_seq, credit_balances, rerating_jobs, einvoice_documents, payment_links,
  portal_links, promotion_reversals TO billing_system;
GRANT SELECT, INSERT, DELETE ON invoice_lines, saga_events, credit_applications, credit_notes, invoice_payments, tenant_event_outbox, credit_ledger, outbox TO billing_system;
GRANT SELECT, INSERT, UPDATE, DELETE ON credit_lots, credit_note_seq TO billing_system;
GRANT SELECT, INSERT ON inbox_replies, expense_inbox_items TO billing_system;
GRANT SELECT ON credit_packs, credit_settings, dunning_settings, expense_inboxes TO billing_system;
GRANT SELECT, DELETE ON expenses TO billing_system;
-- A credit note's refund moves through these columns only.
GRANT UPDATE (refund_state, refund_attempt, refund_run_from, refund_generation, refund_command_id, refund_requested_at, refund_ref, refund_error) ON credit_notes TO billing_system;
-- The relay claims with FOR UPDATE SKIP LOCKED, which needs an UPDATE privilege on a column.
GRANT UPDATE (id) ON outbox TO billing_system;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO billing_system;

-- +goose Down
DROP TABLE expense_rules, expense_inbox_items, expense_inboxes, promotion_reversals, expenses, einvoice_documents, credit_settings, credit_ledger, credit_lots, credit_packs,
  portal_links, invoice_payments, payment_links, tenant_event_outbox, dunning_settings, credit_notes, credit_note_seq, outbox, credit_applications, credit_balances,
  rerating_jobs, inbox_replies, saga_events, invoice_sagas, invoice_number_seq, invoice_lines, invoices, billing_runs;
DROP FUNCTION credit_notes_check_series();
DROP FUNCTION app_tenant_id();
