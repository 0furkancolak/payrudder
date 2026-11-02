-- TRUNCATE of the financial tables (docs/plans/22 G3, "Not covered"). The row triggers of 00003 do not fire on TRUNCATE and nobody but the table owner holds the
-- privilege, but a migration, a script or a person at a psql prompt is exactly who this is for: the tables below are never truncated, not even in a transaction that names
-- a sandbox reset (it deletes row by row). One exception, the one the product has: a tenant move into a cell (pkg/pgxx Move) empties the destination schema before it copies
-- the rows, and that schema is the tenant's own, named by the setting (t_ and the tenant id without dashes); a table of the pooled database (public) is never truncated.
-- A statement trigger also fires for the tables a TRUNCATE ... CASCADE reaches.
-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION financial_table_no_truncate() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_TABLE_SCHEMA = 't_' || replace(current_setting('payrudder.tenant_move', true), '-', '') THEN
    RETURN NULL;
  END IF;
  RAISE EXCEPTION 'rows of % are never truncated: a correction is a new row', TG_TABLE_NAME USING ERRCODE = 'P0001', HINT = 'financial_table_truncate';
END $$;
-- +goose StatementEnd
CREATE TRIGGER invoices_no_truncate BEFORE TRUNCATE ON invoices FOR EACH STATEMENT EXECUTE FUNCTION financial_table_no_truncate();
CREATE TRIGGER invoice_lines_no_truncate BEFORE TRUNCATE ON invoice_lines FOR EACH STATEMENT EXECUTE FUNCTION financial_table_no_truncate();
CREATE TRIGGER credit_notes_no_truncate BEFORE TRUNCATE ON credit_notes FOR EACH STATEMENT EXECUTE FUNCTION financial_table_no_truncate();
CREATE TRIGGER invoice_payments_no_truncate BEFORE TRUNCATE ON invoice_payments FOR EACH STATEMENT EXECUTE FUNCTION financial_table_no_truncate();
CREATE TRIGGER credit_ledger_no_truncate BEFORE TRUNCATE ON credit_ledger FOR EACH STATEMENT EXECUTE FUNCTION financial_table_no_truncate();
CREATE TRIGGER credit_applications_no_truncate BEFORE TRUNCATE ON credit_applications FOR EACH STATEMENT EXECUTE FUNCTION financial_table_no_truncate();
CREATE TRIGGER saga_events_no_truncate BEFORE TRUNCATE ON saga_events FOR EACH STATEMENT EXECUTE FUNCTION financial_table_no_truncate();

-- +goose Down
DROP TRIGGER saga_events_no_truncate ON saga_events;
DROP TRIGGER credit_applications_no_truncate ON credit_applications;
DROP TRIGGER credit_ledger_no_truncate ON credit_ledger;
DROP TRIGGER invoice_payments_no_truncate ON invoice_payments;
DROP TRIGGER credit_notes_no_truncate ON credit_notes;
DROP TRIGGER invoice_lines_no_truncate ON invoice_lines;
DROP TRIGGER invoices_no_truncate ON invoices;
DROP FUNCTION financial_table_no_truncate();
