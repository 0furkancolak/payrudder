# 05 — Data model

Database-per-service on one Postgres 17 instance locally (separate databases, separate roles).
Migrations live in `services/<svc>/migrations` (`goose`, SQL files), queries in
`services/<svc>/internal/store/queries/*.sql` compiled by `sqlc` against `pgx/v5`.

## Common conventions

| Convention | Rule |
|---|---|
| IDs | `uuid` (UUIDv7, generated in Go, time-ordered → index-friendly). Event IDs are ULID strings (`text`) |
| Tenant column | Every tenant-owned table has `tenant_id uuid NOT NULL` as the **first** column of its PK or of a leading index |
| Time | `timestamptz`, always UTC |
| Money | `bigint` minor units (`*_minor`). Unit prices `numeric(30,12)` ([ADR 0010](adr/0010-money-and-quantity-representation.md)) |
| Quantities | Scaled integer units of the meter (`quantity × 10^quantity_scale`): `bigint` per event, `numeric(38,0)` for aggregates ([ADR 0010](adr/0010-money-and-quantity-representation.md)) |
| Period | `period_id text` `'YYYY-MM'`, a calendar month **in the tenant's billing time zone**, with `CHECK (period_id ~ '^\d{4}-(0[1-9]|1[0-2])$')` |
| Soft delete | Not used. Append-only versions instead |
| Roles | `<svc>_app` (RLS enforced, used by request handlers), `<svc>_system` (`BYPASSRLS`, used by consumers, relays, schedulers), `<svc>_migrator` (owner) |

### RLS template

Applied to every tenant-owned table:

```sql
ALTER TABLE <t> ENABLE ROW LEVEL SECURITY;
ALTER TABLE <t> FORCE ROW LEVEL SECURITY;          -- applies to the owner too
CREATE POLICY tenant_isolation ON <t>
  USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON <t> TO <svc>_app;
```

`current_setting(..., true)` returns NULL when the setting was never defined in the session, but **`''`** on a pooled
connection where an earlier transaction set it locally (found by the Phase 01 pgxx integration test). `''::uuid` would raise
an error, so the policy wraps it in `NULLIF(…, '')`. Either way, a forgotten tenant context returns **zero rows** instead of
erroring or leaking. The Go helper `pkg/pgxx.InTenantTx(ctx, pool, tenantID, fn)`
is the only way handlers open a transaction. It runs `SELECT set_config('app.tenant_id', $1, true)` first.
Details: [06-multi-tenancy.md](06-multi-tenancy.md#row-level-security).

---

## `plans_db`

```sql
-- Every database defines app_tenant_id() = NULLIF(current_setting('app.tenant_id', true), '')::uuid; policies use it.
-- plans_app reads its own tenant row only through SECURITY DEFINER current_tenant(). Implemented: services/plans-svc/migrations.

CREATE TABLE accounts (                               -- the customer of Quotify; owns one sandbox + one live tenant
  id          uuid PRIMARY KEY,
  name        text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tenants (                                -- the isolation unit everywhere (RLS, partition key)
  id            uuid PRIMARY KEY,
  account_id    uuid NOT NULL REFERENCES accounts(id),
  environment   text NOT NULL CHECK (environment IN ('sandbox','live')),
  name          text NOT NULL,
  tier          text NOT NULL CHECK (tier IN ('free','standard','enterprise')),
  currency      char(3) NOT NULL,                     -- chosen at creation, immutable (trigger), from the supported list
  timezone      text NOT NULL,                        -- IANA name, chosen at creation, immutable; defines period boundaries
  grace_period  interval NOT NULL CHECK (grace_period BETWEEN '0' AND '7 days'),  -- chosen at creation, mutable, snapshotted per period
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (account_id, environment)
);  -- not RLS'd: operator-only via AdminService (plans_system role)

CREATE TABLE api_keys (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  prefix      char(8) NOT NULL UNIQUE,
  key_hash    bytea NOT NULL,                       -- sha256(secret)
  env         text NOT NULL CHECK (env IN ('live','test')),  -- must match the tenant: test ↔ sandbox, live ↔ live
  status      text NOT NULL CHECK (status IN ('active','revoked')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  revoked_at  timestamptz
);
CREATE INDEX ON api_keys (tenant_id);

CREATE TABLE customers (
  tenant_id    uuid NOT NULL,
  id           uuid NOT NULL,
  external_id  text NOT NULL,
  name         text NOT NULL,
  email        text NOT NULL,
  payment_method text,                               -- opaque PSP token; in sandbox one of the pm_sandbox_* scenario tokens
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, external_id)
);

CREATE TABLE meters (
  tenant_id       uuid NOT NULL,
  code            text NOT NULL,                     -- e.g. 'ai_tokens'
  version         int  NOT NULL,
  aggregation     text NOT NULL CHECK (aggregation IN ('SUM','COUNT','MAX')),
  value_property  text,                              -- NULL for COUNT
  quantity_scale  smallint NOT NULL CHECK (quantity_scale BETWEEN 0 AND 6),  -- decimal places; same for all versions of a code
  unit_label      text NOT NULL,                     -- 'GB-h', 'tokens'; shown on invoices
  filter          jsonb,                             -- {"model": ["gpt-x"]} equality-only
  created_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, code, version)
);

CREATE TABLE plans (
  tenant_id  uuid NOT NULL, id uuid NOT NULL, code text NOT NULL, name text NOT NULL,
  PRIMARY KEY (tenant_id, id), UNIQUE (tenant_id, code)
);

CREATE TABLE plan_versions (
  tenant_id       uuid NOT NULL,
  id              uuid NOT NULL,
  plan_id         uuid NOT NULL,
  version         int  NOT NULL,
  base_fee_minor  bigint NOT NULL DEFAULT 0 CHECK (base_fee_minor >= 0),
  published_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, plan_id, version),
  FOREIGN KEY (tenant_id, plan_id) REFERENCES plans (tenant_id, id)
);

CREATE TABLE plan_prices (
  tenant_id        uuid NOT NULL,
  plan_version_id  uuid NOT NULL,
  meter_code       text NOT NULL,
  model            text NOT NULL CHECK (model IN ('PER_UNIT','TIERED_GRADUATED','PACKAGE')),
  config           jsonb NOT NULL,   -- validated in Go; e.g. {"tiers":[{"up_to":10000,"unit_price":"0"},{"up_to":null,"unit_price":"0.002"}]}
  PRIMARY KEY (tenant_id, plan_version_id, meter_code)
);

CREATE TABLE plan_entitlements (
  tenant_id            uuid NOT NULL,
  plan_version_id      uuid NOT NULL,
  meter_code           text NOT NULL,
  limit_units          bigint NOT NULL CHECK (limit_units >= 0),      -- scaled units of the meter; 0 = unlimited
  -- plan_prices.config stores quantities as scaled units too: {"tiers":[{"up_to_units":10500,"unit_price":"0","flat_fee_minor":0},…]}
  alert_thresholds_pct int[]  NOT NULL DEFAULT '{80,100}',
  PRIMARY KEY (tenant_id, plan_version_id, meter_code)
);

CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE TABLE subscriptions (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  customer_id      uuid NOT NULL,
  plan_version_id  uuid NOT NULL,
  active           tstzrange NOT NULL,                -- [starts_at, ends_at)
  created_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  EXCLUDE USING gist (tenant_id WITH =, customer_id WITH =, active WITH &&)  -- one active sub per customer
  -- Phase 13 (migration 00004):
  -- state text ('trialing','active','past_due','paused','cancelled'), interval text ('month'; 'year' is refused for now),
  -- currency char(3) NOT NULL (fixed for the subscription), trial_ends_at, cancel_at_period_end, paused_at,
  -- billing_anchor timestamptz NOT NULL (start of the calendar month it began in), changed_from_id (the subscription a plan change replaced)
);

-- Phase 13: customers gain country char(2) and billing_currency char(3), both optional.
CREATE TABLE plan_version_fees (            -- fixed fee in a currency other than the tenant's
  tenant_id uuid NOT NULL, plan_version_id uuid NOT NULL, currency char(3) NOT NULL, base_fee_minor bigint NOT NULL,
  PRIMARY KEY (tenant_id, plan_version_id, currency)
);
CREATE TABLE plan_currency_prices (         -- usage prices in such a currency, same shape as plan_prices
  tenant_id uuid NOT NULL, plan_version_id uuid NOT NULL, currency char(3) NOT NULL, meter_code text NOT NULL,
  model text NOT NULL, config jsonb NOT NULL,
  PRIMARY KEY (tenant_id, plan_version_id, currency, meter_code)
);

CREATE TABLE outbox (                                  -- shared outbox shape, see ingest_db
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id uuid NOT NULL, topic text NOT NULL, partition_key text NOT NULL,
  payload bytea, headers jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now()
);
```

RLS: on all tables except `accounts`, `tenants`, `api_keys` (accessed only by `plans_system` via AdminService/ResolveApiKey) and `outbox`.

---

## `ingest_db`

```sql
CREATE TABLE idempotency_keys (
  tenant_id        uuid NOT NULL,
  idempotency_key  text NOT NULL,
  event_id         text NOT NULL,                     -- ULID
  payload_hash     bytea NOT NULL,                    -- sha256 of canonical event; detects key reuse
  created_at       timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, idempotency_key, created_at)  -- see note
) PARTITION BY RANGE (created_at);
-- monthly partitions created ahead by a maintenance job; dropped after 45 days

CREATE TABLE outbox (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id      uuid NOT NULL,
  topic          text NOT NULL,                       -- 'usage.events.v1'
  partition_key  text NOT NULL,                       -- tenant_id
  payload        bytea NOT NULL,                      -- serialized UsageEvent
  headers        jsonb NOT NULL,                      -- traceparent, ce-type, ...
  ingested_at    timestamptz NOT NULL
);
CREATE INDEX outbox_ingested_at ON outbox (ingested_at);  -- GetOutboxWatermark = SELECT min(ingested_at)
```

**Partitioned-PK note.** Postgres requires the partition key to be in a partitioned table's PK. So uniqueness
is on `(tenant_id, idempotency_key, created_at)`, which **isn't** uniqueness on the key alone.
Resolution: `created_at` isn't `now()` but the **start of the dedup window bucket**, i.e. `date_trunc('month', now())`.
A key is then unique within its month's partition, and the insert path checks the two previous months' partitions too
(`ON CONFLICT` covers the current month, plus an explicit `SELECT` against the two before it). The dedup window is therefore
"≥ 2 months, < 3 months" (35 days can span three calendar months), which covers the 35-day max lateness. Covered by a unit + integration test.
(Alternative: unpartitioned table plus a batched `DELETE` job. Simpler, but `DELETE` bloat at volume. Decided: partitions. The benchmark of both is recorded in [ADR 0014](adr/0014-idempotency-key-retention.md): dropping a month took 24 ms against 8.4 s of batched deletes plus a vacuum.)

RLS: `idempotency_keys` RLS'd for `ingest_app` (SELECT, INSERT only). `outbox` accessed only by `ingest_system` (relay: SELECT, DELETE and
`UPDATE (id)`, which `FOR UPDATE SKIP LOCKED` needs), with insert for `ingest_app` only through the `SECURITY DEFINER` function
`enqueue_outbox(tenant, topic, partition_key, payloads[], headers[], ingested_at)`, which rejects any tenant but the caller's so handlers can't
read or forge other tenants' rows. `maintain_idempotency_partitions(now, ahead, retention)` creates and drops the monthly partitions
and is executable by `ingest_system` only.

---

## `aggregator_db`

```sql
CREATE TABLE aggregate_generations (                  -- generations are per tenant
  tenant_id    uuid NOT NULL,
  generation   int  NOT NULL,
  status       text NOT NULL CHECK (status IN ('building','catching_up','active','retired','discarded')),
  created_at   timestamptz NOT NULL DEFAULT now(),
  promoted_at  timestamptz,
  PRIMARY KEY (tenant_id, generation)
);
CREATE UNIQUE INDEX one_active_generation ON aggregate_generations (tenant_id) WHERE status = 'active';
CREATE UNIQUE INDEX one_open_replay       ON aggregate_generations (tenant_id) WHERE status IN ('building','catching_up');
-- a tenant without a row reads/writes generation 1 (no row needed until its first replay)

CREATE TABLE usage_aggregates (
  tenant_id    uuid NOT NULL,
  customer_id  uuid NOT NULL,
  meter_code   text NOT NULL,
  period_id    text NOT NULL,
  generation   int  NOT NULL,
  quantity     numeric(38,0) NOT NULL,               -- scaled units
  quantity_scale smallint NOT NULL,                  -- the meter's scale (immutable), to render the decimal
  event_count  bigint NOT NULL,
  updated_at   timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, period_id, customer_id, meter_code, generation)
);

CREATE TABLE late_usage (
  tenant_id         uuid NOT NULL,
  customer_id       uuid NOT NULL,
  meter_code        text NOT NULL,
  origin_period_id  text NOT NULL,                     -- period by event_time
  billed_period_id  text NOT NULL,                     -- period by ingested_at (tenant time zone)
  generation        int  NOT NULL,
  quantity          numeric(38,0) NOT NULL,
  quantity_scale    smallint NOT NULL,
  event_count       bigint NOT NULL,
  updated_at        timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, billed_period_id, customer_id, meter_code, origin_period_id, generation)
);

CREATE TABLE processed_events (
  tenant_id   uuid NOT NULL,
  event_id    text NOT NULL,
  generation  int  NOT NULL,
  period_id   text NOT NULL,                           -- partition key: period by event_time
  PRIMARY KEY (period_id, tenant_id, event_id, generation)
) PARTITION BY LIST (period_id);
-- partitions per month; dropped when period_end + 45 days < now()

CREATE TABLE consumer_offsets (
  consumer_group  text NOT NULL,                       -- 'aggregator' or 'aggregator-replay-<job>'
  topic           text NOT NULL,
  partition       int  NOT NULL,
  next_offset     bigint NOT NULL,
  updated_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (consumer_group, topic, partition)
);

CREATE TABLE period_settings (                         -- settings snapshot per tenant+period
  tenant_id     uuid NOT NULL,
  period_id     text NOT NULL,
  timezone      text NOT NULL,
  period_start  timestamptz NOT NULL,                  -- local midnight on the 1st, as UTC instant
  period_end    timestamptz NOT NULL,
  grace_period  interval NOT NULL,
  cutoff_at     timestamptz NOT NULL,                  -- period_end + grace; sandbox early close may lower it (aggregator.md)
  early_closed_at timestamptz,                         -- sandbox only
  frozen_at     timestamptz,                           -- set by billing via FreezePeriod after rating
  PRIMARY KEY (tenant_id, period_id)
);

CREATE TABLE replay_jobs (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL,                      -- one job per tenant; "all tenants" = a batch of jobs
  from_period_id   text NOT NULL,
  to_period_id     text NOT NULL,
  target_generation int NOT NULL,                      -- FK (tenant_id, target_generation) → aggregate_generations
  status           text NOT NULL CHECK (status IN ('pending','running','verifying','ready','promoted','failed')),
  start_offsets    jsonb,                              -- partition → offset (from ListOffsetsAfterMilli)
  end_offsets      jsonb,                              -- snapshot of end offsets at start
  dual_write_from  jsonb,                              -- partition → first live offset written to both generations (catch-up)
  reason           text NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now()
);
```

`period_id` is the leading PK column of `processed_events` because of `LIST` partitioning. Lookups always know the period
(it's derived from `event_time`), so the index is still selective.

RLS: `usage_aggregates`, `late_usage`, `period_settings` and `aggregate_generations` are RLS'd for `aggregator_app` (read APIs, `FreezePeriod`,
`ClosePeriodEarly`; its only UPDATE is on `period_settings(cutoff_at, early_closed_at, frozen_at)`). `processed_events` and `replay_jobs` carry
`tenant_id` but are consumer-only and not RLS'd (allowlisted in the CI check). Consumers use `aggregator_system`; `FOR SHARE` needs an UPDATE
privilege, hence its UPDATE grant on `period_settings` and `aggregate_generations`. Inbox partitions are named `processed_events_YYYY_MM`, created
on demand by `ensure_processed_partition(period)` and dropped by `drop_old_processed_partitions(now)` (period end + 1 day of slack + 45 days).
Reads always filter `generation = active_generation(tenant_id)` (the tenant's `active` row, else 1), or the
generation pinned by a billing run. The live consumer caches each tenant's active and catching-up generation and refreshes it
inside the batch transaction (`FOR SHARE` on the tenant's generation rows), so a promotion can't race a batch.

---

## `billing_db`

```sql
CREATE TABLE billing_runs (
  tenant_id         uuid NOT NULL,
  period_id         text NOT NULL,
  status            text NOT NULL CHECK (status IN ('SCHEDULED','WAITING_WATERMARK','RATING','INVOICING','COMPLETED','FAILED')),
  cutoff_at         timestamptz NOT NULL,
  generation        int,                               -- aggregate generation used; pinned at RATING
  required_offsets  jsonb,
  started_at        timestamptz, completed_at timestamptz,
  PRIMARY KEY (tenant_id, period_id)
);

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
  status           text NOT NULL,                      -- mirrors saga coarse state: draft/open/paid/uncollectible/void
  version          int NOT NULL DEFAULT 1,
  pdf_object_key   text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  finalized_at     timestamptz,
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, number)
);
CREATE UNIQUE INDEX one_regular_invoice ON invoices (tenant_id, customer_id, period_id) WHERE kind = 'REGULAR';

CREATE TABLE invoice_lines (
  tenant_id        uuid NOT NULL,
  invoice_id       uuid NOT NULL,
  line_no          int  NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('BASE_FEE','USAGE','LATE_USAGE','CREDIT','CORRECTION')),
  meter_code       text,
  origin_period_id text,                               -- for LATE_USAGE
  quantity         numeric(38,0),                      -- scaled units; displayed with the meter's scale + unit_label
  quantity_scale   smallint,
  unit_price       numeric(30,12),                     -- display only for tiered
  amount_minor     bigint NOT NULL,                    -- may be negative for CREDIT
  description      text NOT NULL,
  PRIMARY KEY (tenant_id, invoice_id, line_no)
);

CREATE TABLE invoice_number_seq (
  tenant_id uuid NOT NULL, year int NOT NULL, next_value bigint NOT NULL,
  PRIMARY KEY (tenant_id, year)
);  -- SELECT ... FOR UPDATE at finalize → gap-free per tenant/year

CREATE TABLE invoice_sagas (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,                      -- saga_id
  invoice_id       uuid NOT NULL,
  state            text NOT NULL,                      -- see billing-svc state machine
  version          int  NOT NULL,                      -- optimistic concurrency
  attempt          int  NOT NULL DEFAULT 0,
  step_deadline    timestamptz,
  dunning_attempt  int  NOT NULL DEFAULT 0,
  next_attempt_at  timestamptz,
  last_error       text,
  updated_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, invoice_id)
);
CREATE INDEX saga_deadlines ON invoice_sagas (step_deadline) WHERE state LIKE '%\_REQUESTED';
CREATE INDEX saga_dunning   ON invoice_sagas (next_attempt_at) WHERE state = 'PAYMENT_DECLINED';

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

CREATE TABLE outbox (                                  -- the shared pkg/outbox shape + message_id
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id uuid NOT NULL,
  topic text NOT NULL,                                 -- exchange (rabbitmq) or Kafka topic
  partition_key text NOT NULL,                         -- routing key (rabbitmq) or record key (kafka)
  payload bytea, headers jsonb NOT NULL DEFAULT '{}',  -- headers.x-destination = 'rabbitmq' | 'kafka'
  message_id uuid NOT NULL UNIQUE,                     -- command id / fact id: re-issuing is a no-op
  ingested_at timestamptz NOT NULL DEFAULT now()
);

-- As built, also: billing_runs.invoice_count/last_error/created_at; invoices.snapshot jsonb (customer + tenant data the
-- commands need); invoice_lines.unit_label; invoice_sagas.step_attempt. See services/billing-svc/migrations.
```

RLS on all tenant tables for `billing_app`. Scheduler, sweeper, reply consumer and relay use `billing_system`.

---

## `assets_db`

```sql
CREATE TABLE upload_sessions (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  idempotency_key  text,
  status           text NOT NULL CHECK (status IN ('OPEN','COMPLETED','EXPIRED','ABORTED')),
  expires_at       timestamptz NOT NULL,                -- created_at + 24 h
  created_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, idempotency_key)
);
CREATE INDEX upload_sessions_expiry ON upload_sessions (expires_at) WHERE status = 'OPEN';

CREATE TABLE assets (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  session_id       uuid,                                -- NULL for proxied form-data uploads
  purpose          text NOT NULL CHECK (purpose IN ('invoice_logo','invoice_attachment','general')),
  filename         text NOT NULL,                       -- sanitized, display only
  content_type     text NOT NULL,
  declared_size    bigint,
  size_bytes       bigint,                              -- from HeadObject after completion
  sha256           bytea,                               -- declared or computed
  object_key       text NOT NULL,                       -- {tenant_id}/assets/{id}
  s3_upload_id     text,                                -- multipart UploadId while uploading
  part_size        bigint,
  part_count       int,
  status           text NOT NULL CHECK (status IN ('PENDING','UPLOADING','SCANNING','READY','REJECTED','ABORTED','CORRUPT','DELETED')),
  reject_reason    text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  completed_at     timestamptz,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, session_id) REFERENCES upload_sessions (tenant_id, id)
);
CREATE INDEX assets_by_session ON assets (tenant_id, session_id);

CREATE TABLE storage_usage (                            -- quota accounting, updated in the completion transaction
  tenant_id uuid PRIMARY KEY, bytes_used bigint NOT NULL DEFAULT 0 CHECK (bytes_used >= 0)
);
```

RLS on all three for `assets_app`. Sweeper (expiry → `AbortMultipartUpload`), verifier and orphan reconciler run as `assets_system`.
Quota check: `UPDATE storage_usage SET bytes_used = bytes_used + $size WHERE tenant_id = $t AND bytes_used + $size <= $quota`
(0 rows → `STORAGE_QUOTA_EXCEEDED`), in the same transaction that marks the asset `READY`.

---

## Worker stores

```sql
-- payments_db
CREATE TABLE payment_attempts (
  command_id uuid PRIMARY KEY, tenant_id uuid NOT NULL, invoice_id uuid NOT NULL,
  environment text NOT NULL CHECK (environment IN ('sandbox','live')), provider text NOT NULL,   -- sandbox ⇒ provider = 'mockpsp'
  idempotency_key text NOT NULL, kind text NOT NULL CHECK (kind IN ('charge','refund','status')),
  amount_minor bigint, status text NOT NULL, psp_response jsonb, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON payment_attempts (tenant_id, invoice_id);

-- notifier_db
CREATE TABLE sent_notifications (
  dedup_key text PRIMARY KEY, tenant_id uuid NOT NULL, template text NOT NULL,
  status text NOT NULL CHECK (status IN ('SENDING','SENT')), smtp_message_id text,
  created_at timestamptz NOT NULL DEFAULT now(), sent_at timestamptz
);
```

```sql
-- payments_db, phase 30: e-invoice integrator accounts (sealed) and the idempotency record of issued documents
CREATE TABLE einvoice_accounts (
  tenant_id uuid NOT NULL, environment text NOT NULL CHECK (environment IN ('sandbox','live')), provider text NOT NULL, mode text NOT NULL CHECK (mode IN ('test','production')),
  settings_enc bytea NOT NULL, PRIMARY KEY (tenant_id, environment)          -- AES-256-GCM, bound to tenant|einvoice|environment
);
CREATE TABLE einvoice_issuances (
  document_id uuid PRIMARY KEY, tenant_id uuid NOT NULL, environment text NOT NULL, provider text NOT NULL, kind text NOT NULL, document_type text NOT NULL,
  status text NOT NULL CHECK (status IN ('started','issued','cancelled')), legal_number text, external_id text NOT NULL DEFAULT '', xml_key text, pdf_key text
);
CREATE UNIQUE INDEX ON einvoice_issuances (tenant_id, environment, legal_number) WHERE legal_number IS NOT NULL;   -- a legal number is never reused
```

```sql
-- billing_db, phase 30 (RLS on tenant_id like every tenant table): what the tenant sees of an e-document
CREATE TABLE einvoice_documents (
  tenant_id uuid NOT NULL, id uuid NOT NULL,                       -- id = uuidv5(tenant|kind|source id) = the UBL UUID
  kind text NOT NULL CHECK (kind IN ('invoice','credit_note')), invoice_id uuid NOT NULL, credit_note_id uuid, number text NOT NULL,
  status text NOT NULL CHECK (status IN ('pending','issued','failed','cancelled')), attempts int NOT NULL, streak int NOT NULL, next_attempt_at timestamptz,
  legal_number text, external_id text NOT NULL DEFAULT '', xml_key text, pdf_key text, request bytea NOT NULL,   -- request = the frozen IssueEInvoice command
  PRIMARY KEY (tenant_id, id)
);
```

Workers are internal and single-purpose, and are not RLS'd: every row is keyed by a command ID from billing-svc.
`tenant_id` is still stored for audit and per-tenant metrics.

## Index rationale (hot paths)

| Query | Index |
|---|---|
| Ingest dedup | PK `idempotency_keys (tenant_id, idempotency_key, created_at)` |
| Outbox relay | PK `outbox (id)` + `SKIP LOCKED`. Watermark via `outbox_ingested_at` |
| Aggregator dedup | PK `processed_events (period_id, tenant_id, event_id, generation)` |
| Aggregator upsert | PK `usage_aggregates` |
| Billing stream period usage | PK prefix `(tenant_id, period_id)` |
| Saga timeout sweeper | partial `saga_deadlines` |
| Dunning scheduler | partial `saga_dunning` |
