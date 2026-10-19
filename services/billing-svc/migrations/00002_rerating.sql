-- Re-rating (Phase 11): job progress, and the one-per-job guards that make a repeated or resumed job a no-op.

-- +goose Up
ALTER TABLE rerating_jobs
  ADD COLUMN customers_rated    int    NOT NULL DEFAULT 0,
  ADD COLUMN corrections        int    NOT NULL DEFAULT 0,
  ADD COLUMN credits            int    NOT NULL DEFAULT 0,
  ADD COLUMN correction_minor   bigint NOT NULL DEFAULT 0,
  ADD COLUMN credit_minor       bigint NOT NULL DEFAULT 0,
  ADD COLUMN last_error         text,
  ADD COLUMN completed_at       timestamptz;
ALTER TABLE rerating_jobs ADD CONSTRAINT rerating_status CHECK (status IN ('RUNNING', 'COMPLETED', 'FAILED'));

-- A correction invoice and a credit balance belong to the job that created them: at most one per customer.
ALTER TABLE invoices ADD COLUMN rerating_job_id uuid;
CREATE UNIQUE INDEX one_correction_per_job ON invoices (tenant_id, customer_id, rerating_job_id) WHERE rerating_job_id IS NOT NULL;
ALTER TABLE credit_balances ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
CREATE UNIQUE INDEX one_credit_per_job ON credit_balances (tenant_id, customer_id, source_rerating_job_id);
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
ALTER TABLE credit_applications ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_applications FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_applications USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT ON credit_applications TO billing_system;
GRANT SELECT ON credit_applications TO billing_app;
GRANT SELECT ON credit_balances, rerating_jobs TO billing_app;

-- +goose Down
DROP TABLE credit_applications;
DROP INDEX one_credit_per_job, one_correction_per_job;
ALTER TABLE credit_balances DROP COLUMN created_at;
ALTER TABLE invoices DROP COLUMN rerating_job_id;
ALTER TABLE rerating_jobs DROP CONSTRAINT rerating_status;
ALTER TABLE rerating_jobs
  DROP COLUMN completed_at, DROP COLUMN last_error, DROP COLUMN credit_minor, DROP COLUMN correction_minor,
  DROP COLUMN credits, DROP COLUMN corrections, DROP COLUMN customers_rated;
