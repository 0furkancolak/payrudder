-- Phase 36: buying credit packs. A payment link of kind 'credit_pack' sells one pack to one customer: its amount, currency and credits are copied from the pack when
-- the link is made (never taken from the client), and when the invoice made from it is paid the saga grants the credits once. A pack may carry a validity (the
-- lots bought from it expire after that many days), and a tenant may set the balance below which a customer is told they are running low.
-- +goose Up
ALTER TABLE credit_packs ADD COLUMN validity_days int CHECK (validity_days IS NULL OR validity_days BETWEEN 1 AND 3650);   -- NULL: the credits never expire

ALTER TABLE payment_links
  ADD COLUMN pack_id       uuid,
  ADD COLUMN quantity      int CHECK (quantity IS NULL OR quantity BETWEEN 1 AND 1000),
  ADD COLUMN credits       bigint CHECK (credits IS NULL OR credits > 0),       -- credits one use grants: the pack's credits times the quantity, frozen at creation
  ADD COLUMN validity_days int CHECK (validity_days IS NULL OR validity_days BETWEEN 1 AND 3650);
ALTER TABLE payment_links DROP CONSTRAINT payment_links_kind_check;
ALTER TABLE payment_links ADD CONSTRAINT payment_links_kind_check CHECK (kind IN ('invoice', 'adhoc', 'credit_pack'));
ALTER TABLE payment_links DROP CONSTRAINT payment_link_shape;
ALTER TABLE payment_links ADD CONSTRAINT payment_link_shape CHECK (
  (kind = 'invoice' AND invoice_id IS NOT NULL AND amount_minor IS NULL AND pack_id IS NULL)
  OR (kind = 'adhoc' AND invoice_id IS NULL AND amount_minor IS NOT NULL AND currency IS NOT NULL AND parent_id IS NULL AND pack_id IS NULL)
  OR (kind = 'credit_pack' AND invoice_id IS NULL AND amount_minor IS NOT NULL AND currency IS NOT NULL AND parent_id IS NULL AND pack_id IS NOT NULL
      AND quantity IS NOT NULL AND credits IS NOT NULL AND customer_id IS NOT NULL));
-- The open link a customer's portal already made for a pack, so that pressing "Buy" twice leads to one invoice.
CREATE INDEX payment_links_portal_purchase ON payment_links (tenant_id, customer_id, pack_id, quantity) WHERE kind = 'credit_pack' AND idempotency_key LIKE 'portal:%';

-- The portal buys on behalf of its customer, and an invoice is made out by the customer's external id.
ALTER TABLE portal_links ADD COLUMN customer_external_id text;

-- When a customer's balance falls below low_threshold, billing tells the tenant (credits.low). No row, or 0: off.
CREATE TABLE credit_settings (
  tenant_id     uuid PRIMARY KEY,
  low_threshold bigint NOT NULL CHECK (low_threshold >= 0),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE credit_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_settings USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, UPDATE ON credit_settings TO billing_app;
GRANT SELECT ON credit_settings TO billing_system;

-- A consume (billing_app) announces credits.low in its own transaction, like the saga does as billing_system.
GRANT INSERT ON tenant_event_outbox TO billing_app;

-- +goose Down
ALTER TABLE portal_links DROP COLUMN customer_external_id;
REVOKE INSERT ON tenant_event_outbox FROM billing_app;
DROP TABLE credit_settings;
DROP INDEX payment_links_portal_purchase;
DELETE FROM payment_links WHERE kind = 'credit_pack' OR parent_id IN (SELECT id FROM payment_links WHERE kind = 'credit_pack');
ALTER TABLE payment_links DROP CONSTRAINT payment_link_shape;
ALTER TABLE payment_links ADD CONSTRAINT payment_link_shape CHECK (
  (kind = 'invoice' AND invoice_id IS NOT NULL AND amount_minor IS NULL)
  OR (kind = 'adhoc' AND invoice_id IS NULL AND amount_minor IS NOT NULL AND currency IS NOT NULL AND parent_id IS NULL));
ALTER TABLE payment_links DROP CONSTRAINT payment_links_kind_check;
ALTER TABLE payment_links ADD CONSTRAINT payment_links_kind_check CHECK (kind IN ('invoice', 'adhoc'));
ALTER TABLE payment_links DROP COLUMN validity_days, DROP COLUMN credits, DROP COLUMN quantity, DROP COLUMN pack_id;
ALTER TABLE credit_packs DROP COLUMN validity_days;
