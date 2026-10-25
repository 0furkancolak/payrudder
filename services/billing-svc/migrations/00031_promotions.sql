-- Phase 46: promotion codes on payment links, hosted checkout and credit purchases.
--
-- The coupon and its redemption live in plans-svc. Here an invoice remembers which coupon it used (so a void can give the redemption back, and the
-- accounting reports can name it), a credit-pack invoice remembers the bonus credits its coupon adds to the lot, and a link may carry the code an operator chose
-- for it. A void queues the reversal in the same transaction as the state change; a scheduler pass tells plans-svc.
-- +goose Up
ALTER TABLE payment_links ADD COLUMN promotion_code text CHECK (promotion_code IS NULL OR promotion_code ~ '^[A-Za-z0-9_-]{2,40}$');

ALTER TABLE invoices
  ADD COLUMN coupon_code      text,
  ADD COLUMN promotion_code   text,
  ADD COLUMN promotion_target text CHECK (promotion_target IN ('credit_purchase', 'payment')),
  ADD COLUMN bonus_credits    bigint NOT NULL DEFAULT 0 CHECK (bonus_credits >= 0),
  ADD CONSTRAINT invoice_promotion_shape CHECK ((coupon_code IS NULL) = (promotion_target IS NULL));

CREATE TABLE promotion_reversals (
  tenant_id   uuid NOT NULL,
  invoice_id  uuid NOT NULL,
  target_kind text NOT NULL CHECK (target_kind IN ('credit_purchase', 'payment')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  attempts    int NOT NULL DEFAULT 0,
  done_at     timestamptz,
  PRIMARY KEY (tenant_id, invoice_id),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);
CREATE INDEX promotion_reversals_pending ON promotion_reversals (created_at) WHERE done_at IS NULL;
ALTER TABLE promotion_reversals ENABLE ROW LEVEL SECURITY;
ALTER TABLE promotion_reversals FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON promotion_reversals USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT ON promotion_reversals TO billing_app;
GRANT SELECT, INSERT, UPDATE ON promotion_reversals TO billing_system;

-- +goose Down
DROP TABLE promotion_reversals;
ALTER TABLE invoices DROP CONSTRAINT invoice_promotion_shape, DROP COLUMN bonus_credits, DROP COLUMN promotion_target, DROP COLUMN promotion_code, DROP COLUMN coupon_code;
ALTER TABLE payment_links DROP COLUMN promotion_code;
