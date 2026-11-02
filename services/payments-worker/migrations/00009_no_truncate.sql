-- TRUNCATE of the payment ledger and the receipts recorded by hand (docs/plans/22 G3, "Not covered"). The row trigger of 00002 does not fire on TRUNCATE and nobody but the
-- table owner holds the privilege, but a migration, a script or a person at a psql prompt is exactly who this is for: neither table is truncated, not even in a transaction
-- that names a sandbox reset (it deletes row by row). One exception, the one the product has: a tenant move into a cell (pkg/pgxx Move) empties the destination schema before
-- it copies the rows, and that schema is the tenant's own, named by the setting (t_ and the tenant id without dashes); a table of the pooled database (public) is never
-- truncated. A statement trigger also fires for the tables a TRUNCATE ... CASCADE reaches.
-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION ledger_no_truncate() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_TABLE_SCHEMA = 't_' || replace(current_setting('payrudder.tenant_move', true), '-', '') THEN
    RETURN NULL;
  END IF;
  RAISE EXCEPTION 'rows of % are never truncated: a correction is a new row', TG_TABLE_NAME USING ERRCODE = 'P0001', HINT = 'ledger_table_truncate';
END $$;
-- +goose StatementEnd
CREATE TRIGGER payment_ledger_no_truncate BEFORE TRUNCATE ON payment_ledger FOR EACH STATEMENT EXECUTE FUNCTION ledger_no_truncate();
CREATE TRIGGER manual_receipts_no_truncate BEFORE TRUNCATE ON manual_receipts FOR EACH STATEMENT EXECUTE FUNCTION ledger_no_truncate();

-- +goose Down
DROP TRIGGER manual_receipts_no_truncate ON manual_receipts;
DROP TRIGGER payment_ledger_no_truncate ON payment_ledger;
DROP FUNCTION ledger_no_truncate();
