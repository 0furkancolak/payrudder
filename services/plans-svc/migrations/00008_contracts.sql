-- Contracts for the seller's customers (docs/plans/21-contracts.md): the agreements a seller presents (distance sales, pre-information, terms, privacy), the
-- evidence that a customer saw and accepted a specific text, and the customer's withdrawal requests.
--
-- contract_versions is immutable: an edit is the next version of the series (kind, audience). The newest version of a series is the current one; there is no
-- mutable "current" pointer. contract_acceptances is append-only: no UPDATE and no DELETE for the application role; the erasure job (plans_system) may blank
-- ip and user_agent after the retention period, and nothing else; a sandbox tenant's rows may be deleted by a reset (like the affiliate journal), and so may a
-- live tenant's once the directory says it has a cell (the last step of moving it out of the pooled database). Business rules are in internal/contracts.

-- +goose Up
CREATE TABLE contract_versions (
  tenant_id   uuid NOT NULL,
  id          uuid NOT NULL,
  kind        text NOT NULL CHECK (kind IN ('distance_sales', 'pre_info', 'tos', 'privacy')),
  audience    text NOT NULL DEFAULT 'consumer' CHECK (audience IN ('consumer', 'business')),
  number      integer NOT NULL CHECK (number >= 1),
  material    boolean NOT NULL,
  -- language (en, tr, de, fr, es, ar) to the text (restricted markdown with {{variables}}).
  bodies      jsonb NOT NULL CHECK (jsonb_typeof(bodies) = 'object' AND bodies <> '{}'::jsonb),
  sha256      text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  note        text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  author      text NOT NULL DEFAULT '' CHECK (length(author) <= 200),
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, kind, audience, number)
);

CREATE TABLE contract_settings (
  tenant_id        uuid PRIMARY KEY,
  required_kinds   text[] NOT NULL DEFAULT '{}' CHECK (required_kinds <@ ARRAY['distance_sales', 'pre_info', 'tos', 'privacy']),
  immediate_start  boolean NOT NULL DEFAULT false,
  -- 36: Mesafeli Sözleşmeler Yönetmeliği art. 20(1) says three years for the record of each transaction. How long the IP address may be kept is the seller's call.
  retention_months integer NOT NULL DEFAULT 36 CHECK (retention_months BETWEEN 12 AND 120),
  updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE contract_acceptances (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  -- One checkout moment: the versions accepted together share it.
  group_id         uuid NOT NULL,
  -- NULL for a guest payment (a payment link without a customer): context_ref is then the invoice.
  customer_id      uuid,
  version_id       uuid NOT NULL,
  kind             text NOT NULL,
  audience         text NOT NULL,
  version_number   integer NOT NULL,
  version_sha256   text NOT NULL,
  locale_requested text NOT NULL DEFAULT '',
  locale_served    text NOT NULL,
  body_sha256      text NOT NULL,
  context_kind     text NOT NULL CHECK (context_kind IN ('hosted_invoice', 'api')),
  context_ref      text NOT NULL DEFAULT '' CHECK (length(context_ref) <= 200),
  source           text NOT NULL CHECK (source IN ('hosted', 'api')),
  -- Which parts the platform observed itself (ip, user_agent, time, text) and which the seller claimed (the API path).
  observed         jsonb NOT NULL,
  checkbox_text    text NOT NULL CHECK (length(checkbox_text) BETWEEN 1 AND 1000),
  immediate_start_consent         boolean NOT NULL DEFAULT false,
  immediate_start_text            text NOT NULL DEFAULT '' CHECK (length(immediate_start_text) <= 1000),
  withdrawal_waiver_acknowledged  boolean NOT NULL DEFAULT false,
  withdrawal_waiver_text          text NOT NULL DEFAULT '' CHECK (length(withdrawal_waiver_text) <= 1000),
  variables        jsonb NOT NULL DEFAULT '{}',
  snapshot_sha256  text NOT NULL CHECK (snapshot_sha256 ~ '^[0-9a-f]{64}$'),
  ip               text NOT NULL DEFAULT '' CHECK (length(ip) <= 64),
  user_agent       text NOT NULL DEFAULT '' CHECK (length(user_agent) <= 500),
  accepted_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
  FOREIGN KEY (tenant_id, version_id) REFERENCES contract_versions (tenant_id, id),
  CONSTRAINT contract_acceptance_guest_has_ref CHECK (customer_id IS NOT NULL OR context_ref <> ''),
  CONSTRAINT contract_acceptance_waiver_needs_consent CHECK (NOT withdrawal_waiver_acknowledged OR immediate_start_consent)
);
CREATE INDEX contract_acceptances_by_customer ON contract_acceptances (tenant_id, customer_id, kind, version_number DESC) WHERE customer_id IS NOT NULL;
CREATE INDEX contract_acceptances_by_ref ON contract_acceptances (tenant_id, context_ref) WHERE customer_id IS NULL;
CREATE INDEX contract_acceptances_by_group ON contract_acceptances (tenant_id, group_id);
CREATE INDEX contract_acceptances_to_erase ON contract_acceptances (accepted_at) WHERE ip <> '' OR user_agent <> '';

-- Append-only. The only UPDATE is the erasure job's: ip and user_agent become empty, and nothing else changes. The DELETE is for a reset of a sandbox and for a tenant
-- that has a cell (see affiliate_attributions_immutable; the cells' own copy of this function, in migrations/tenant, reads the same facts from the tenant's row setting).
-- +goose StatementBegin
CREATE FUNCTION contract_acceptances_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND EXISTS (SELECT 1 FROM tenants WHERE id = OLD.tenant_id AND (environment = 'sandbox' OR cell <> '')) THEN
    RETURN OLD;
  END IF;
  IF TG_OP = 'UPDATE' AND NEW.ip = '' AND NEW.user_agent = '' AND (to_jsonb(NEW) - 'ip' - 'user_agent') = (to_jsonb(OLD) - 'ip' - 'user_agent') THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'a contract acceptance is evidence: it is never changed or deleted' USING ERRCODE = 'P0001', HINT = 'contract_acceptance_append_only';
END $$;
-- +goose StatementEnd
CREATE TRIGGER contract_acceptances_append_only BEFORE UPDATE OR DELETE ON contract_acceptances
  FOR EACH ROW EXECUTE FUNCTION contract_acceptances_append_only();

-- The page each acceptance group was shown to and accepted, as written then: the evidence is these bytes, not a page written again later (the wording of a label may change
-- in a release, the page that was hashed must not). One row for a group; never changed, deleted only like the acceptances.
CREATE TABLE contract_snapshots (
  tenant_id  uuid NOT NULL,
  group_id   uuid NOT NULL,
  html       text NOT NULL,
  sha256     text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, group_id)
);

-- +goose StatementBegin
CREATE FUNCTION contract_snapshots_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND EXISTS (SELECT 1 FROM tenants WHERE id = OLD.tenant_id AND (environment = 'sandbox' OR cell <> '')) THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'a contract snapshot is evidence: it is never changed or deleted' USING ERRCODE = 'P0001', HINT = 'contract_snapshot_append_only';
END $$;
-- +goose StatementEnd
CREATE TRIGGER contract_snapshots_append_only BEFORE UPDATE OR DELETE ON contract_snapshots
  FOR EACH ROW EXECUTE FUNCTION contract_snapshots_append_only();


-- A customer's withdrawal from a contract (the 14-day right): recorded when the statement arrives; the seller decides and refunds. invoice_id and subscription_id
-- belong to billing and to the subscription module, so there is no foreign key to them.
CREATE TABLE contract_withdrawals (
  tenant_id       uuid NOT NULL,
  id              uuid NOT NULL,
  customer_id     uuid NOT NULL,
  subscription_id uuid,
  invoice_id      uuid,
  statement       text NOT NULL DEFAULT '' CHECK (length(statement) <= 2000),
  source          text NOT NULL CHECK (source IN ('api', 'console', 'hosted')),
  status          text NOT NULL DEFAULT 'received' CHECK (status IN ('received', 'accepted', 'rejected', 'refunded')),
  received_at     timestamptz NOT NULL DEFAULT now(),
  decided_at      timestamptz,
  decided_by      text NOT NULL DEFAULT '' CHECK (length(decided_by) <= 200),
  decision_note   text NOT NULL DEFAULT '' CHECK (length(decision_note) <= 1000),
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
  CONSTRAINT contract_withdrawal_decided CHECK ((status = 'received') = (decided_at IS NULL))
);
CREATE INDEX contract_withdrawals_by_customer ON contract_withdrawals (tenant_id, customer_id, received_at DESC);
CREATE INDEX contract_withdrawals_by_status ON contract_withdrawals (tenant_id, status, received_at DESC);

ALTER TABLE contract_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE contract_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON contract_versions USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
ALTER TABLE contract_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE contract_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON contract_settings USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
ALTER TABLE contract_acceptances ENABLE ROW LEVEL SECURITY;
ALTER TABLE contract_acceptances FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON contract_acceptances USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
ALTER TABLE contract_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE contract_snapshots FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON contract_snapshots USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
ALTER TABLE contract_withdrawals ENABLE ROW LEVEL SECURITY;
ALTER TABLE contract_withdrawals FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON contract_withdrawals USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

-- plans_app: versions and acceptances are insert-only; settings and withdrawals change.
GRANT SELECT, INSERT ON contract_versions, contract_acceptances, contract_snapshots TO plans_app;
GRANT SELECT, INSERT, UPDATE ON contract_settings, contract_withdrawals TO plans_app;
-- plans_system: reads (promotion, the erasure job), the erasure job's one UPDATE, and the reset of a sandbox (the acceptances go before the customers they name).
GRANT SELECT ON contract_versions, contract_settings TO plans_system;
GRANT SELECT, DELETE ON contract_acceptances, contract_snapshots, contract_withdrawals TO plans_system;
GRANT UPDATE (ip, user_agent) ON contract_acceptances TO plans_system;

-- +goose Down
DROP TABLE contract_withdrawals, contract_snapshots, contract_acceptances, contract_settings, contract_versions;
DROP FUNCTION contract_snapshots_append_only(), contract_acceptances_append_only();
