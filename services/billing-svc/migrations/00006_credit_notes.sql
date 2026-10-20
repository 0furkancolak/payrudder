-- Phase 22: credit notes. An issued invoice is never edited; a correction is a credit note, numbered like invoices (gap-free per tenant and year),
-- whose amount goes to the customer's credit balance.
-- +goose Up
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
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, number),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);
CREATE INDEX credit_notes_by_invoice ON credit_notes (tenant_id, invoice_id);
CREATE INDEX credit_notes_by_customer ON credit_notes (tenant_id, customer_id, created_at DESC);

-- A credit balance comes from exactly one source: a re-rating job, a plan change's adjustment, or a credit note.
ALTER TABLE credit_balances ADD COLUMN source_credit_note_id uuid;
ALTER TABLE credit_balances DROP CONSTRAINT credit_has_one_source;
ALTER TABLE credit_balances ADD CONSTRAINT credit_has_one_source CHECK (num_nonnulls(source_rerating_job_id, source_adjustment_id, source_credit_note_id) = 1);
CREATE UNIQUE INDEX one_credit_per_credit_note ON credit_balances (tenant_id, source_credit_note_id) WHERE source_credit_note_id IS NOT NULL;

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['credit_notes', 'credit_note_seq'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd
GRANT SELECT ON credit_notes TO billing_app;
GRANT SELECT, INSERT ON credit_notes TO billing_system;
GRANT SELECT, INSERT, UPDATE ON credit_note_seq TO billing_system;

-- +goose Down
DROP INDEX one_credit_per_credit_note;
ALTER TABLE credit_balances DROP CONSTRAINT credit_has_one_source;
DELETE FROM credit_balances WHERE source_credit_note_id IS NOT NULL;
ALTER TABLE credit_balances DROP COLUMN source_credit_note_id;
ALTER TABLE credit_balances ADD CONSTRAINT credit_has_one_source CHECK ((source_rerating_job_id IS NULL) <> (source_adjustment_id IS NULL));
DROP TABLE credit_notes, credit_note_seq;
