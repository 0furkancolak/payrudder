-- Financial history is immutable in the database, not only by grants (docs/plans/22 G3). The grants say who may write; these triggers say what, so that a bug in a
-- role that may update or delete (billing_system) still cannot rewrite what was invoiced.
--
-- 1. An invoice that has left draft keeps its amounts, its number, its customer, its period and its snapshot. What still changes on it is its status (the saga),
--    its PDF key, the payment method the customer chose (snapshot.payment_method) and the promotion columns: exactly SetInvoiceStatus, SetInvoicePDFKeyBySaga,
--    SetInvoicePaymentMethod and SetInvoicePromotion. SetInvoiceTotals and OpenInvoice (number, finalized_at) run while the invoice is still a draft.
-- 2. The rows of the financial tables are never deleted, except in a transaction that names the tenant (set_config(..., true)) as one of two things:
--    payrudder.sandbox_reset: internal/sandboxreset resetting a sandbox tenant; an invoice of the live environment is refused even then;
--    payrudder.tenant_move: pkg/pgxx Move deleting the originals after the rows were copied to the tenant's new place.
--    A voided invoice also gives back the credit it consumed: its credit_applications go (the invoice is void by then, in the same transaction).
-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION invoices_frozen() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'draft'
     OR ROW(NEW.tenant_id, NEW.id, NEW.customer_id, NEW.period_id, NEW.kind, NEW.plan_version_id, NEW.currency, NEW.subtotal_minor, NEW.total_minor, NEW.number,
            NEW.created_at, NEW.finalized_at)
        IS DISTINCT FROM
        ROW(OLD.tenant_id, OLD.id, OLD.customer_id, OLD.period_id, OLD.kind, OLD.plan_version_id, OLD.currency, OLD.subtotal_minor, OLD.total_minor, OLD.number,
            OLD.created_at, OLD.finalized_at)
     OR (NEW.snapshot - 'payment_method') IS DISTINCT FROM (OLD.snapshot - 'payment_method') THEN
    RAISE EXCEPTION 'an invoice that is no longer a draft keeps its amounts, number, customer, period and snapshot' USING ERRCODE = 'P0001', HINT = 'invoice_frozen';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER invoices_frozen BEFORE UPDATE ON invoices FOR EACH ROW WHEN (OLD.status <> 'draft') EXECUTE FUNCTION invoices_frozen();

-- +goose StatementBegin
CREATE FUNCTION financial_row_no_delete() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF current_setting('payrudder.tenant_move', true) = OLD.tenant_id::text THEN
    RETURN OLD;
  END IF;
  IF current_setting('payrudder.sandbox_reset', true) = OLD.tenant_id::text THEN
    IF TG_TABLE_NAME <> 'invoices' THEN
      RETURN OLD;
    END IF;
    IF OLD.snapshot ->> 'environment' = 'sandbox' THEN
      RETURN OLD;
    END IF;
  END IF;
  -- Nested, not one condition: plpgsql resolves the fields of OLD when it prepares a statement, and only credit_applications has an invoice_id.
  IF TG_TABLE_NAME = 'credit_applications' THEN
    IF EXISTS (SELECT 1 FROM invoices WHERE tenant_id = OLD.tenant_id AND id = OLD.invoice_id AND status = 'void') THEN
      RETURN OLD;
    END IF;
  END IF;
  RAISE EXCEPTION 'rows of % are never deleted: a correction is a new row', TG_TABLE_NAME USING ERRCODE = 'P0001', HINT = 'financial_row_delete';
END $$;
-- +goose StatementEnd
CREATE TRIGGER invoices_no_delete BEFORE DELETE ON invoices FOR EACH ROW EXECUTE FUNCTION financial_row_no_delete();
CREATE TRIGGER invoice_lines_no_delete BEFORE DELETE ON invoice_lines FOR EACH ROW EXECUTE FUNCTION financial_row_no_delete();
CREATE TRIGGER credit_notes_no_delete BEFORE DELETE ON credit_notes FOR EACH ROW EXECUTE FUNCTION financial_row_no_delete();
CREATE TRIGGER invoice_payments_no_delete BEFORE DELETE ON invoice_payments FOR EACH ROW EXECUTE FUNCTION financial_row_no_delete();
CREATE TRIGGER credit_ledger_no_delete BEFORE DELETE ON credit_ledger FOR EACH ROW EXECUTE FUNCTION financial_row_no_delete();
CREATE TRIGGER credit_applications_no_delete BEFORE DELETE ON credit_applications FOR EACH ROW EXECUTE FUNCTION financial_row_no_delete();
CREATE TRIGGER saga_events_no_delete BEFORE DELETE ON saga_events FOR EACH ROW EXECUTE FUNCTION financial_row_no_delete();

-- +goose Down
DROP TRIGGER saga_events_no_delete ON saga_events;
DROP TRIGGER credit_applications_no_delete ON credit_applications;
DROP TRIGGER credit_ledger_no_delete ON credit_ledger;
DROP TRIGGER invoice_payments_no_delete ON invoice_payments;
DROP TRIGGER credit_notes_no_delete ON credit_notes;
DROP TRIGGER invoice_lines_no_delete ON invoice_lines;
DROP TRIGGER invoices_no_delete ON invoices;
DROP FUNCTION financial_row_no_delete();
DROP TRIGGER invoices_frozen ON invoices;
DROP FUNCTION invoices_frozen();
