-- A legal document is cancelled, not erased (docs/plans/22, review finding 8). einvoice_issuances holds the e-invoices and e-credit-notes an integrator accepted, with their
-- legal numbers; the ledger, the receipts and the reconciliation tables already refuse every DELETE, and this one did not. It refuses it the same way: only a transaction that
-- names the tenant (set_config(..., true)) in
--   payrudder.sandbox_reset: internal/sandboxreset resetting a sandbox tenant, and then only a row of the sandbox environment (a live document is refused even then);
--   payrudder.tenant_move: pkg/pgxx Move deleting the originals after the rows were copied to the tenant's new place,
-- may delete. Updates are not restricted: a document's status moves from started to issued to cancelled in the same row.
-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION einvoice_issuances_no_delete() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF current_setting('payrudder.tenant_move', true) = OLD.tenant_id::text THEN
    RETURN OLD;
  END IF;
  IF current_setting('payrudder.sandbox_reset', true) = OLD.tenant_id::text AND OLD.environment = 'sandbox' THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'rows of % are never deleted: a document is cancelled, not erased', TG_TABLE_NAME USING ERRCODE = 'P0001', HINT = 'ledger_row_delete';
END $$;
-- +goose StatementEnd
CREATE TRIGGER einvoice_issuances_no_delete BEFORE DELETE ON einvoice_issuances FOR EACH ROW EXECUTE FUNCTION einvoice_issuances_no_delete();

-- +goose Down
DROP TRIGGER einvoice_issuances_no_delete ON einvoice_issuances;
DROP FUNCTION einvoice_issuances_no_delete();
