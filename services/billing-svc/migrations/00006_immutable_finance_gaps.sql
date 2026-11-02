-- What 00003 left open (docs/plans/22 G3, "Not covered"), and the one table of the request path that no tenant guard covered.
--
-- 1. The promotion columns of an invoice that has left draft (coupon_code, promotion_code, promotion_target, bonus_credits) are frozen like its money: they decide
--    which redemption a void gives back and what the accounting reports name. SetInvoicePromotion, their only writer, runs in the transaction that creates the
--    draft (hosted/command/linkinvoice.go), so nothing legitimate changes them later.
-- 2. A line is only added to a draft. Every writer of invoice_lines (InsertLines) runs in the transaction that creates the draft invoice, so an invoice that is open,
--    paid or void takes no new line. A tenant move copies a tenant's lines after its invoices, and its transactions name the tenant (payrudder.tenant_move), as the
--    delete triggers of 00003 expect.
-- 3. expense_inboxes gets the tenant policy the other tenant tables have. billing_app, the only role of the request path that writes it, could write or read the inbox
--    of any tenant; billing_system (BYPASSRLS) still looks a token up without a tenant when mail arrives.
-- +goose Up

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices_frozen() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'draft'
     OR ROW(NEW.tenant_id, NEW.id, NEW.customer_id, NEW.period_id, NEW.kind, NEW.plan_version_id, NEW.currency, NEW.subtotal_minor, NEW.total_minor, NEW.number,
            NEW.created_at, NEW.finalized_at, NEW.coupon_code, NEW.promotion_code, NEW.promotion_target, NEW.bonus_credits)
        IS DISTINCT FROM
        ROW(OLD.tenant_id, OLD.id, OLD.customer_id, OLD.period_id, OLD.kind, OLD.plan_version_id, OLD.currency, OLD.subtotal_minor, OLD.total_minor, OLD.number,
            OLD.created_at, OLD.finalized_at, OLD.coupon_code, OLD.promotion_code, OLD.promotion_target, OLD.bonus_credits)
     OR (NEW.snapshot - 'payment_method') IS DISTINCT FROM (OLD.snapshot - 'payment_method') THEN
    RAISE EXCEPTION 'an invoice that is no longer a draft keeps its amounts, number, customer, period, promotion and snapshot' USING ERRCODE = 'P0001', HINT = 'invoice_frozen';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION invoice_lines_draft_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF current_setting('payrudder.tenant_move', true) = NEW.tenant_id::text THEN
    RETURN NEW;
  END IF;
  IF EXISTS (SELECT 1 FROM invoices WHERE tenant_id = NEW.tenant_id AND id = NEW.invoice_id AND status <> 'draft') THEN
    RAISE EXCEPTION 'a line is added to a draft: an invoice that is no longer a draft keeps its lines' USING ERRCODE = 'P0001', HINT = 'invoice_lines_frozen';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER invoice_lines_draft_only BEFORE INSERT ON invoice_lines FOR EACH ROW EXECUTE FUNCTION invoice_lines_draft_only();

ALTER TABLE expense_inboxes ENABLE ROW LEVEL SECURITY;
ALTER TABLE expense_inboxes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON expense_inboxes USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

-- +goose Down
DROP POLICY tenant_isolation ON expense_inboxes;
ALTER TABLE expense_inboxes NO FORCE ROW LEVEL SECURITY;
ALTER TABLE expense_inboxes DISABLE ROW LEVEL SECURITY;

DROP TRIGGER invoice_lines_draft_only ON invoice_lines;
DROP FUNCTION invoice_lines_draft_only();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invoices_frozen() RETURNS trigger
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
