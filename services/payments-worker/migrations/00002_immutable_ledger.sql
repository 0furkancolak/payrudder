-- The payment ledger and the receipts recorded by hand are immutable in the database, not only by grants (docs/plans/22 G3). payments_system may insert but has no UPDATE
-- on them, and it holds DELETE only for the sandbox reset; this trigger takes that back for everything but a transaction that names the tenant (set_config(..., true)) as:
--   payrudder.sandbox_reset: internal/sandboxreset resetting a sandbox tenant; a row of the live environment is refused even then (a receipt has the environment of its
--     ledger row, which goes after it);
--   payrudder.tenant_move: pkg/pgxx Move deleting the originals after the rows were copied to the tenant's new place.
-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION ledger_no_delete() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  env text;
BEGIN
  IF current_setting('payrudder.tenant_move', true) = OLD.tenant_id::text THEN
    RETURN OLD;
  END IF;
  IF current_setting('payrudder.sandbox_reset', true) = OLD.tenant_id::text THEN
    IF TG_TABLE_NAME = 'payment_ledger' THEN
      env := OLD.environment;
    ELSE
      SELECT environment INTO env FROM payment_ledger WHERE id = OLD.ledger_id;
    END IF;
    IF env = 'sandbox' THEN
      RETURN OLD;
    END IF;
  END IF;
  RAISE EXCEPTION 'rows of % are never deleted: a correction is a new row', TG_TABLE_NAME USING ERRCODE = 'P0001', HINT = 'ledger_row_delete';
END $$;
-- +goose StatementEnd
CREATE TRIGGER payment_ledger_no_delete BEFORE DELETE ON payment_ledger FOR EACH ROW EXECUTE FUNCTION ledger_no_delete();
CREATE TRIGGER manual_receipts_no_delete BEFORE DELETE ON manual_receipts FOR EACH ROW EXECUTE FUNCTION ledger_no_delete();

-- +goose Down
DROP TRIGGER manual_receipts_no_delete ON manual_receipts;
DROP TRIGGER payment_ledger_no_delete ON payment_ledger;
DROP FUNCTION ledger_no_delete();
