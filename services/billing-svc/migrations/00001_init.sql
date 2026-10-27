-- billing_db schema (docs/05-data-model.md#billing_db).
-- This is the baseline: it was written once, before the first release, from the schema the pre-release migrations had built, and it starts the history.
-- Every change from here on is a new numbered migration (00002_...), never an edit of this file.
-- Roles: billing_migrator owns everything; billing_app (read APIs, RLS enforced); billing_system (scheduler, sweeper,
-- reply consumer, relay and the admin/sandbox RPCs: BYPASSRLS).

-- +goose Up

-- Tenant of the current transaction, set by pgxx.InTenantTx.
-- +goose StatementBegin
CREATE FUNCTION app_tenant_id() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE
AS $$ SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid $$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Billing runs, invoices and the payment saga
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

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

-- kind: REGULAR is the period's invoice; CORRECTION is made by re-rating; FEE is a fixed fee invoiced in advance, its own invoice, one per subscription and fee period,
-- found again by fee_key; LINK is the invoice made from an ad-hoc payment link.
-- rerating_job_id: a correction invoice belongs to the job that created it, at most one per customer.
-- provider, country: reports group invoices by the payment provider and the customer's country, so both are columns (the snapshot keeps them too).
-- invoice_number_series: the sandbox number series is enforced by the database, not by convention. A sandbox tenant's invoices are numbered SBX-2026-000123 and its
-- credit notes SBX-CN-2026-000007; a live tenant's never start with SBX-. The environment is the one frozen into the invoice snapshot. A number of the wrong series,
-- or an invoice that names no environment, cannot be written whatever the code did.
-- coupon_code, promotion_code, promotion_target, bonus_credits: the coupon the invoice used (so a void can give the redemption back, and the accounting reports can name
-- it), and the bonus credits its coupon adds to the lot of a credit-pack invoice. The coupon and its redemption live in plans-svc.
CREATE TABLE invoices (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  customer_id      uuid NOT NULL,
  period_id        text NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('REGULAR','CORRECTION','FEE','LINK')),
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
  CONSTRAINT invoice_number_series CHECK (
    coalesce(snapshot ->> 'environment' IN ('sandbox', 'live'), false)
    AND (number IS NULL OR (number LIKE 'SBX-%') = (snapshot ->> 'environment' = 'sandbox'))
  )
);
CREATE UNIQUE INDEX one_regular_invoice ON invoices (tenant_id, customer_id, period_id) WHERE kind = 'REGULAR';
CREATE INDEX invoices_by_customer ON invoices (tenant_id, customer_id, period_id);
CREATE UNIQUE INDEX one_correction_per_job ON invoices (tenant_id, customer_id, rerating_job_id) WHERE rerating_job_id IS NOT NULL;
CREATE UNIQUE INDEX one_fee_invoice ON invoices (tenant_id, fee_key) WHERE fee_key IS NOT NULL;
CREATE INDEX invoices_report ON invoices (tenant_id, period_id, currency);
-- The dashboard's open-invoice ageing reads the open invoices of a tenant; a partial index keeps that read to the open ones however many invoices are paid.
CREATE INDEX invoices_open ON invoices (tenant_id, finalized_at) WHERE status = 'open';

-- Invoice lines can be discounts and tax, and say which service period they cover. A DISCOUNT line that a coupon made names the coupon, so the accounting reports
-- can show gross, discounts and net per coupon; other DISCOUNT lines (what a plan change moves back) have no coupon.
CREATE TABLE invoice_lines (
  tenant_id        uuid NOT NULL,
  invoice_id       uuid NOT NULL,
  line_no          int  NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('BASE_FEE','USAGE','LATE_USAGE','CREDIT','CORRECTION','DISCOUNT','TAX')),
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
-- The income side of the accounting reads the moment an invoice was paid or refunded from the saga history.
CREATE INDEX saga_events_paid ON saga_events (tenant_id, to_state, at) WHERE to_state IN ('PAID', 'REFUNDED');

CREATE TABLE inbox_replies (message_id uuid PRIMARY KEY, received_at timestamptz NOT NULL DEFAULT now());

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Credit balances (from re-rating, plan changes, credit notes and voided invoices) and re-rating
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- A credit balance has a currency (money in two currencies never mixes) and comes from exactly one source: a re-rating job, a plan change's adjustment, a credit
-- note, or a voided invoice that was partly paid by hand (invoice_payments, no card to go back to; one per voided invoice). A re-rating credit has the currency of the
-- customer's regular invoice of the period.
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
  CONSTRAINT credit_has_one_source CHECK (num_nonnulls(source_rerating_job_id, source_adjustment_id, source_credit_note_id, source_invoice_id) = 1)
);
CREATE UNIQUE INDEX one_credit_per_job ON credit_balances (tenant_id, customer_id, source_rerating_job_id);
CREATE UNIQUE INDEX one_credit_per_adjustment ON credit_balances (tenant_id, source_adjustment_id) WHERE source_adjustment_id IS NOT NULL;
CREATE UNIQUE INDEX one_credit_per_credit_note ON credit_balances (tenant_id, source_credit_note_id) WHERE source_credit_note_id IS NOT NULL;
CREATE UNIQUE INDEX one_credit_per_voided_invoice ON credit_balances (tenant_id, source_invoice_id) WHERE source_invoice_id IS NOT NULL;

-- Re-rating: job progress, and the one-per-job guards that make a repeated or resumed job a no-op.
CREATE TABLE rerating_jobs (
  tenant_id uuid NOT NULL, id uuid NOT NULL, period_id text NOT NULL, plan_version_id uuid NOT NULL,
  generation int NOT NULL, status text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
  customers_rated    int    NOT NULL DEFAULT 0,
  corrections        int    NOT NULL DEFAULT 0,
  credits            int    NOT NULL DEFAULT 0,
  correction_minor   bigint NOT NULL DEFAULT 0,
  credit_minor       bigint NOT NULL DEFAULT 0,
  last_error         text,
  completed_at       timestamptz,
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, period_id, plan_version_id),      -- idempotent re-rating
  CONSTRAINT rerating_status CHECK (status IN ('RUNNING', 'COMPLETED', 'FAILED'))
);

-- A credit can be consumed over several invoices (it is applied up to what the invoice costs); the ledger of those applications
-- is what the remainder is computed from. credit_balances.applied_invoice_id is set when the last of it is consumed.
-- A voided invoice gives back the credit balances it consumed: its applications are deleted and the lots are open again.
CREATE TABLE credit_applications (
  tenant_id    uuid   NOT NULL,
  credit_id    uuid   NOT NULL,
  invoice_id   uuid   NOT NULL,
  amount_minor bigint NOT NULL CHECK (amount_minor > 0),
  PRIMARY KEY (tenant_id, credit_id, invoice_id),
  FOREIGN KEY (tenant_id, credit_id) REFERENCES credit_balances (tenant_id, id)
);

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Outbox
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

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

-- Events for the tenant's own systems (invoice.paid, credit_note.created, ...). They are written here in the same transaction as the change they
-- announce and handed to plans-svc, which owns the tenant's webhook endpoints and delivers them. A row is removed once plans-svc has recorded it; the event
-- id makes a repeat harmless.
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

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Credit notes
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

CREATE TABLE credit_note_seq (
  tenant_id uuid NOT NULL, year int NOT NULL, next_value bigint NOT NULL CHECK (next_value >= 1),
  PRIMARY KEY (tenant_id, year)
);

-- An issued invoice is never edited; a correction is a credit note, numbered like invoices (gap-free per tenant and year), whose amount goes to the customer's credit
-- balance. A credit note may instead return its amount to the customer's card (disposition 'refund'): the refund is a RefundPayment command whose reply is matched by
-- command id; billing's tick re-sends it when no reply comes (the provider dedups on the idempotency key).
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

-- A credit note takes the environment of its invoice, which a CHECK cannot see; a trigger reads it.
-- +goose StatementBegin
CREATE FUNCTION credit_notes_check_series() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  env text;
BEGIN
  SELECT snapshot ->> 'environment' INTO env FROM invoices WHERE tenant_id = NEW.tenant_id AND id = NEW.invoice_id;
  IF env IS NULL OR (NEW.number LIKE 'SBX-%') <> (env = 'sandbox') THEN
    RAISE EXCEPTION 'a credit note number must be in the series of its invoice''s environment' USING ERRCODE = 'P0001', HINT = 'number_series';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER credit_notes_series BEFORE INSERT OR UPDATE OF number ON credit_notes FOR EACH ROW EXECUTE FUNCTION credit_notes_check_series();

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Dunning
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- Per-tenant dunning. A tenant may set the delays between a declined charge and each retry; without a row the
-- service-wide defaults apply (+1 d, +3 d, +7 d; minutes in sandbox).
CREATE TABLE dunning_settings (
  tenant_id     uuid PRIMARY KEY,
  retry_hours   int[] NOT NULL CHECK (cardinality(retry_hours) <= 6),  -- delay before retry 1..n, in hours (minutes in a sandbox tenant)
  updated_at    timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Payment links, customer portal links and payments recorded by hand
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- Payment links and the customer payment page. One table, one flow (it began as the invoice link, hence the names of its primary key, unique and check constraints):
-- kind 'invoice' pays an existing invoice; kind 'adhoc' is a fixed amount whose invoice (kind LINK) is made when the payer first presses Continue, and which hands out a
-- child 'invoice' link (parent_id) to continue on; kind 'credit_pack' sells one pack to one customer: its amount, currency and credits are copied from the pack when
-- the link is made (never taken from the client), and when the invoice made from it is paid the saga grants the credits once. A tenant makes an unguessable link and
-- sends it to its customer; the link shows the invoice and its PDF without a sign-in. Only a hash of the token is stored.
-- Visitors create the child link and the counters move when an invoice is paid (the saga runs as billing_system).
CREATE TABLE payment_links (
  tenant_id            uuid NOT NULL,
  id                   uuid NOT NULL,
  invoice_id           uuid,                          -- an ad-hoc link has no invoice until it is used
  token_hash           bytea NOT NULL,
  created_at           timestamptz NOT NULL DEFAULT now(),
  expires_at           timestamptz NOT NULL,
  revoked_at           timestamptz,
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
  pack_id              uuid,
  quantity             int CHECK (quantity IS NULL OR quantity BETWEEN 1 AND 1000),
  credits              bigint CHECK (credits IS NULL OR credits > 0),       -- credits one use grants: the pack's credits times the quantity, frozen at creation
  validity_days        int CHECK (validity_days IS NULL OR validity_days BETWEEN 1 AND 3650),
  promotion_code       text CHECK (promotion_code IS NULL OR promotion_code ~ '^[A-Za-z0-9_-]{2,40}$'),   -- the code an operator chose for the link
  CONSTRAINT invoice_links_pkey PRIMARY KEY (tenant_id, id),
  CONSTRAINT invoice_links_token_hash_key UNIQUE (token_hash),
  CONSTRAINT invoice_links_token_hash_check CHECK (length(token_hash) = 32),
  CONSTRAINT invoice_links_tenant_id_invoice_id_fkey FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id),
  CONSTRAINT payment_link_parent FOREIGN KEY (tenant_id, parent_id) REFERENCES payment_links (tenant_id, id),
  CONSTRAINT payment_link_shape CHECK (
    (kind = 'invoice' AND invoice_id IS NOT NULL AND amount_minor IS NULL AND pack_id IS NULL)
    OR (kind = 'adhoc' AND invoice_id IS NULL AND amount_minor IS NOT NULL AND currency IS NOT NULL AND parent_id IS NULL AND pack_id IS NULL)
    OR (kind = 'credit_pack' AND invoice_id IS NULL AND amount_minor IS NOT NULL AND currency IS NOT NULL AND parent_id IS NULL AND pack_id IS NOT NULL
        AND quantity IS NOT NULL AND credits IS NOT NULL AND customer_id IS NOT NULL))
);
CREATE INDEX payment_links_by_invoice ON payment_links (tenant_id, invoice_id);
CREATE UNIQUE INDEX payment_links_idempotency ON payment_links (tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX payment_links_children ON payment_links (tenant_id, parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX payment_links_expiry ON payment_links (expires_at) WHERE parent_id IS NULL AND revoked_at IS NULL AND expired_notified_at IS NULL;
-- The open link a customer's portal already made for a pack, so that pressing "Buy" twice leads to one invoice.
CREATE INDEX payment_links_portal_purchase ON payment_links (tenant_id, customer_id, pack_id, quantity) WHERE kind = 'credit_pack' AND idempotency_key LIKE 'portal:%';

-- A customer portal link shows one customer's invoices and payments. The portal buys on behalf of its customer, and an invoice is made out by the customer's
-- external id.
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

-- Money received outside a payment provider. An operator records it against an invoice (cash, a bank transfer, other) with who and when; the invoice is
-- paid when the payments add up to the total. Append-only: a correction is another row.
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

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Prepaid credits
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- A tenant sells credit packs; a customer's credits live in lots (one per grant, with an optional expiry) and every change
-- is a row in an append-only ledger. A balance is the sum of the lots that have not expired; consuming takes from the lot that expires first.
-- A pack may carry a validity (the lots bought from it expire after that many days).
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

-- When a customer's balance falls below low_threshold, billing tells the tenant (credits.low). No row, or 0: off.
CREATE TABLE credit_settings (
  tenant_id     uuid PRIMARY KEY,
  low_threshold bigint NOT NULL CHECK (low_threshold >= 0),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- E-invoice documents
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- The legal e-document of an invoice or a credit note and where it stands (docs/phases/phase-30-e-invoice.md).
-- id is uuidv5(tenant|kind|source id): it is also the UBL UUID at the integrator, so one source can never have two documents.
-- request is the frozen IssueEInvoice command; a retry sends the same document (only the parties are refreshed by the operator's retry).
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

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Accounting
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- A tenant records what it spends (a vendor, an amount in one currency, a category, a date; optionally repeating monthly or yearly);
-- income is never stored here, it is read from invoices, their TAX lines and credit notes. A recurring expense is one row: its occurrences are worked out
-- when a summary is asked for. An expense is never deleted, it is voided with a reason.
-- A receipt or invoice can be kept with an expense. The file is a READY asset of the tenant in assets-svc (the id is all billing keeps); the tax is what the
-- receipt shows inside the amount, informational: no summary reads it.
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

-- The invoice inbox: invoices+<token>@<inbound domain>. The token is the address (not a credential: mail to it only adds items to review), so it is kept as is and
-- looked up without a tenant; one per tenant, or per app (scope = the app id, '' for the tenant).
CREATE TABLE expense_inboxes (
  tenant_id  uuid NOT NULL,
  scope      text NOT NULL DEFAULT '',
  token      text NOT NULL UNIQUE CHECK (length(token) BETWEEN 16 AND 64),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, scope)
);

-- What arrived by mail and waits for a person: one row per attachment. The console scans the file in the browser and makes the expense; the server only keeps the bytes.
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

-- Recurring-vendor rules.
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

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Promotions
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- Promotion codes on payment links, hosted checkout and credit purchases. A void queues the reversal in the same transaction as the state change; a scheduler pass
-- tells plans-svc, which gives the redemption back.
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

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Row level security on every tenant-owned table the app role can read. FORCE applies it to the owner too (billing_system has BYPASSRLS).
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

ALTER TABLE billing_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_runs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON billing_runs USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE invoices ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoices USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE invoice_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoice_lines USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE invoice_sagas ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_sagas FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoice_sagas USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE saga_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE saga_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saga_events USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE credit_balances ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_balances FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_balances USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE rerating_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE rerating_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON rerating_jobs USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE credit_applications ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_applications FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_applications USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE credit_notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_notes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_notes USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE credit_note_seq ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_note_seq FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_note_seq USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE dunning_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE dunning_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON dunning_settings USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE tenant_event_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_event_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_event_outbox USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE payment_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payment_links USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE portal_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE portal_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON portal_links USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE invoice_payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_payments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoice_payments USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE credit_packs ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_packs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_packs USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE credit_lots ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_lots FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_lots USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE credit_ledger ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_ledger FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_ledger USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE credit_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_settings USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE einvoice_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE einvoice_documents FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON einvoice_documents USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE expenses ENABLE ROW LEVEL SECURITY;
ALTER TABLE expenses FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON expenses USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE expense_inbox_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE expense_inbox_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON expense_inbox_items USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE expense_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE expense_rules FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON expense_rules USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE promotion_reversals ENABLE ROW LEVEL SECURITY;
ALTER TABLE promotion_reversals FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON promotion_reversals USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Grants
-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------

-- billing_app: the read APIs, and the few tables the request path writes.
GRANT SELECT ON billing_runs, invoices, invoice_lines, invoice_sagas, saga_events, credit_balances, credit_applications, rerating_jobs, credit_notes, invoice_payments,
  einvoice_documents, promotion_reversals TO billing_app;   -- saga_events: the revenue report tells refunded invoices by the saga's history
GRANT SELECT, INSERT, UPDATE ON dunning_settings, payment_links, portal_links, credit_packs, credit_lots, credit_settings, expenses, expense_inboxes, expense_inbox_items
  TO billing_app;
GRANT SELECT, INSERT ON credit_ledger TO billing_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON expense_rules TO billing_app;
-- A consume (billing_app) announces credits.low in its own transaction, like the saga does as billing_system.
GRANT INSERT ON tenant_event_outbox TO billing_app;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO billing_app;

-- billing_system: the scheduler, the saga and the sweepers.
GRANT SELECT, INSERT, UPDATE ON billing_runs, invoices, invoice_sagas, invoice_number_seq, credit_balances, rerating_jobs, credit_note_seq, credit_lots, einvoice_documents,
  payment_links, portal_links, promotion_reversals TO billing_system;
GRANT SELECT, INSERT ON invoice_lines, saga_events, inbox_replies, credit_applications, credit_notes, invoice_payments, credit_ledger, tenant_event_outbox,
  expense_inbox_items, outbox TO billing_system;
GRANT SELECT ON dunning_settings, credit_packs, credit_settings, expenses, expense_inboxes TO billing_system;
-- A credit note's refund moves through these columns only.
GRANT UPDATE (refund_state, refund_attempt, refund_run_from, refund_generation, refund_command_id, refund_requested_at, refund_ref, refund_error) ON credit_notes TO billing_system;
-- The relay claims with FOR UPDATE SKIP LOCKED, which needs an UPDATE privilege on a column.
GRANT UPDATE (id) ON outbox TO billing_system;
-- Deleting: a draft's lines are replaced when its run is retried; a voided invoice gives back the credit it consumed (its applications go); the relay and the tenant
-- event hand-over remove what they sent; and resetting a sandbox deletes a tenant's customer-side data. billing_system may delete only what the reset lists
-- (queries/reset.sql); a test fails when a table that holds tenant rows is neither listed nor kept on purpose.
GRANT DELETE ON invoice_lines, outbox, tenant_event_outbox, credit_applications, credit_ledger, credit_lots, credit_balances, rerating_jobs, saga_events, invoice_sagas,
  invoice_payments, promotion_reversals, einvoice_documents, credit_notes, payment_links, portal_links, invoices, billing_runs, invoice_number_seq, credit_note_seq,
  expenses TO billing_system;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO billing_system;

-- +goose Down
DROP TABLE promotion_reversals, expense_rules, expense_inbox_items, expense_inboxes, expenses, einvoice_documents, credit_settings, credit_ledger, credit_lots, credit_packs,
  invoice_payments, portal_links, payment_links, credit_notes, credit_note_seq, dunning_settings, tenant_event_outbox, outbox, credit_applications, rerating_jobs,
  credit_balances, inbox_replies, saga_events, invoice_sagas, invoice_number_seq, invoice_lines, invoices, billing_runs;
DROP FUNCTION credit_notes_check_series();
DROP FUNCTION app_tenant_id();
