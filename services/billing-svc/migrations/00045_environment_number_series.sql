-- Phase 45 C: the sandbox number series is enforced by the database, not by convention.
--
-- A sandbox tenant's invoices are numbered SBX-2026-000123 and its credit notes SBX-CN-2026-000007; a live tenant's never start with SBX-. The environment is the one
-- frozen into the invoice snapshot. A number of the wrong series, or an invoice that names no environment, cannot be written whatever the code did.

-- +goose Up
ALTER TABLE invoices ADD CONSTRAINT invoice_number_series CHECK (
  coalesce(snapshot ->> 'environment' IN ('sandbox', 'live'), false)
  AND (number IS NULL OR (number LIKE 'SBX-%') = (snapshot ->> 'environment' = 'sandbox'))
) NOT VALID;
ALTER TABLE invoices VALIDATE CONSTRAINT invoice_number_series;

-- A credit note takes the environment of its invoice, which a CHECK cannot see; a trigger reads it.
-- +goose StatementBegin
CREATE FUNCTION credit_notes_check_series() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  env text;
BEGIN
  SELECT snapshot ->> 'environment' INTO env FROM invoices WHERE tenant_id = NEW.tenant_id AND id = NEW.invoice_id;
  IF env IS NULL OR (NEW.number LIKE 'SBX-%') <> (env = 'sandbox') THEN
    RAISE EXCEPTION 'a credit note number must be in the series of its invoice''s environment' USING ERRCODE = 'P0001', HINT = 'number_series';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER credit_notes_series BEFORE INSERT OR UPDATE OF number ON credit_notes FOR EACH ROW EXECUTE FUNCTION credit_notes_check_series();

-- +goose Down
DROP TRIGGER credit_notes_series ON credit_notes;
DROP FUNCTION credit_notes_check_series();
ALTER TABLE invoices DROP CONSTRAINT invoice_number_series;
