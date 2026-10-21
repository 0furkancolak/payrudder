-- Phase 37: accounting. A tenant records what it spends (a vendor, an amount in one currency, a category, a date; optionally repeating monthly or yearly);
-- income is never stored here, it is read from invoices, their TAX lines and credit notes. A recurring expense is one row: its occurrences are worked out
-- when a summary is asked for. An expense is never deleted, it is voided with a reason.
-- +goose Up
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
  PRIMARY KEY (tenant_id, id),
  CHECK (recurring <> 'none' OR recurring_until IS NULL),
  CHECK (recurring_until IS NULL OR recurring_until >= incurred_on),
  CHECK ((voided_at IS NULL) = (void_reason IS NULL))
);
-- The summary and the list read a tenant's live expenses by date.
CREATE INDEX expenses_by_date ON expenses (tenant_id, incurred_on DESC, id) WHERE voided_at IS NULL;

ALTER TABLE expenses ENABLE ROW LEVEL SECURITY;
ALTER TABLE expenses FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON expenses USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, UPDATE ON expenses TO billing_app;
GRANT SELECT ON expenses TO billing_system;

-- The income side reads the moment an invoice was paid or refunded from the saga history.
CREATE INDEX saga_events_paid ON saga_events (tenant_id, to_state, at) WHERE to_state IN ('PAID', 'REFUNDED');

-- +goose Down
DROP INDEX saga_events_paid;
DROP TABLE expenses;
