-- Phase 54: a receipt or invoice kept with an expense. The file is a READY asset of the tenant in assets-svc (the id is all billing keeps); the tax is what the
-- receipt shows inside the amount, informational: no summary reads it.
-- +goose Up
ALTER TABLE expenses
  ADD COLUMN tax_minor bigint NOT NULL DEFAULT 0 CHECK (tax_minor >= 0),
  ADD COLUMN attachment_asset_id uuid;


-- The invoice inbox: invoices+<token>@<inbound domain>. The token is the address (not a credential: mail to it only adds items to review), so it is kept as is and
-- looked up without a tenant; one per tenant, or per app (scope = the app id, '' for the tenant).
CREATE TABLE expense_inboxes (
  tenant_id  uuid NOT NULL,
  scope      text NOT NULL DEFAULT '',
  token      text NOT NULL UNIQUE CHECK (length(token) BETWEEN 16 AND 64),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, scope)
);
GRANT SELECT, INSERT, UPDATE ON expense_inboxes TO billing_app;
GRANT SELECT ON expense_inboxes TO billing_system;

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
ALTER TABLE expense_inbox_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE expense_inbox_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON expense_inbox_items USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, UPDATE ON expense_inbox_items TO billing_app;
GRANT SELECT, INSERT ON expense_inbox_items TO billing_system;

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
ALTER TABLE expense_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE expense_rules FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON expense_rules USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, UPDATE, DELETE ON expense_rules TO billing_app;

-- +goose Down
DROP TABLE expense_rules;
DROP TABLE expense_inbox_items;
DROP TABLE expense_inboxes;
ALTER TABLE expenses DROP COLUMN attachment_asset_id, DROP COLUMN tax_minor;
