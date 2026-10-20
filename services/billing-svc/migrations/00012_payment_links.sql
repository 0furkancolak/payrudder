-- Phase 34: payment links and the customer payment page.
--
-- The phase-23 invoice link becomes the payment link: one table, one flow. kind 'invoice' pays an existing invoice; kind 'adhoc' is a fixed amount whose
-- invoice (kind LINK) is made when the payer first presses Continue, and which hands out a child 'invoice' link (parent_id) to continue on.
-- A customer portal link shows one customer's invoices and payments.
-- +goose Up
ALTER TABLE invoice_links RENAME TO payment_links;
ALTER INDEX invoice_links_by_invoice RENAME TO payment_links_by_invoice;

ALTER TABLE payment_links
  ADD COLUMN kind                 text NOT NULL DEFAULT 'invoice' CHECK (kind IN ('invoice', 'adhoc')),
  ADD COLUMN parent_id            uuid,
  ADD COLUMN single_use           boolean NOT NULL DEFAULT false,
  ADD COLUMN idempotency_key      text,
  ADD COLUMN request_hash         bytea,
  ADD COLUMN amount_minor         bigint CHECK (amount_minor IS NULL OR amount_minor > 0),
  ADD COLUMN currency             char(3),
  ADD COLUMN description          text,
  ADD COLUMN customer_id          uuid,
  ADD COLUMN customer_external_id text,
  ADD COLUMN use_count            int NOT NULL DEFAULT 0,
  ADD COLUMN paid_count           int NOT NULL DEFAULT 0,
  ADD COLUMN last_paid_at         timestamptz,
  ADD COLUMN expired_notified_at  timestamptz;
-- An ad-hoc link has no invoice until it is used.
ALTER TABLE payment_links ALTER COLUMN invoice_id DROP NOT NULL;
ALTER TABLE payment_links ADD CONSTRAINT payment_link_shape CHECK (
  (kind = 'invoice' AND invoice_id IS NOT NULL AND amount_minor IS NULL)
  OR (kind = 'adhoc' AND invoice_id IS NULL AND amount_minor IS NOT NULL AND currency IS NOT NULL AND parent_id IS NULL));
ALTER TABLE payment_links ADD CONSTRAINT payment_link_parent FOREIGN KEY (tenant_id, parent_id) REFERENCES payment_links (tenant_id, id);
CREATE UNIQUE INDEX payment_links_idempotency ON payment_links (tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX payment_links_children ON payment_links (tenant_id, parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX payment_links_expiry ON payment_links (expires_at) WHERE parent_id IS NULL AND revoked_at IS NULL AND expired_notified_at IS NULL;

-- Visitors create the child link and the counters move when an invoice is paid (the saga runs as billing_system).
GRANT INSERT, UPDATE ON payment_links TO billing_system;

-- The invoice made from an ad-hoc link.
ALTER TABLE invoices DROP CONSTRAINT invoices_kind_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_kind_check CHECK (kind IN ('REGULAR','CORRECTION','FEE','LINK'));

CREATE TABLE portal_links (
  tenant_id   uuid NOT NULL,
  id          uuid NOT NULL,
  customer_id uuid NOT NULL,
  token_hash  bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  revoked_at  timestamptz,
  PRIMARY KEY (tenant_id, id)
);
CREATE INDEX portal_links_by_customer ON portal_links (tenant_id, customer_id);
ALTER TABLE portal_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE portal_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON portal_links USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, UPDATE ON portal_links TO billing_app;
GRANT SELECT, INSERT, UPDATE ON portal_links TO billing_system;

-- +goose Down
DROP TABLE portal_links;
ALTER TABLE invoices DROP CONSTRAINT invoices_kind_check;
DELETE FROM invoices WHERE kind = 'LINK';
ALTER TABLE invoices ADD CONSTRAINT invoices_kind_check CHECK (kind IN ('REGULAR','CORRECTION','FEE'));
REVOKE INSERT, UPDATE ON payment_links FROM billing_system;
DELETE FROM payment_links WHERE kind = 'adhoc' OR parent_id IS NOT NULL;
DROP INDEX payment_links_expiry, payment_links_children, payment_links_idempotency;
ALTER TABLE payment_links DROP CONSTRAINT payment_link_parent, DROP CONSTRAINT payment_link_shape;
ALTER TABLE payment_links ALTER COLUMN invoice_id SET NOT NULL;
ALTER TABLE payment_links
  DROP COLUMN expired_notified_at, DROP COLUMN last_paid_at, DROP COLUMN paid_count, DROP COLUMN use_count, DROP COLUMN customer_external_id, DROP COLUMN customer_id,
  DROP COLUMN description, DROP COLUMN currency, DROP COLUMN amount_minor, DROP COLUMN request_hash, DROP COLUMN idempotency_key, DROP COLUMN single_use,
  DROP COLUMN parent_id, DROP COLUMN kind;
ALTER INDEX payment_links_by_invoice RENAME TO invoice_links_by_invoice;
ALTER TABLE payment_links RENAME TO invoice_links;
