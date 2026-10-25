-- Phase 46: a DISCOUNT line that a coupon made names the coupon, so the accounting reports can show gross, discounts and net per coupon. Other DISCOUNT lines (what a
-- plan change moves back) have no coupon.
-- +goose Up
ALTER TABLE invoice_lines ADD COLUMN coupon_code text;
CREATE INDEX invoice_lines_coupon ON invoice_lines (tenant_id, coupon_code, invoice_id) WHERE coupon_code IS NOT NULL;

-- Lines made before this migration said "Coupon CODE" in their description. The migrator owns the table and RLS is forced on it, so it sees no rows without a tenant:
-- the owner is exempt while FORCE is off, and it is on again before the migration ends.
ALTER TABLE invoice_lines NO FORCE ROW LEVEL SECURITY;
UPDATE invoice_lines SET coupon_code = substr(description, 8) WHERE kind = 'DISCOUNT' AND description LIKE 'Coupon %' AND length(description) > 7;
ALTER TABLE invoice_lines FORCE ROW LEVEL SECURITY;

-- +goose Down
DROP INDEX invoice_lines_coupon;
ALTER TABLE invoice_lines DROP COLUMN coupon_code;
