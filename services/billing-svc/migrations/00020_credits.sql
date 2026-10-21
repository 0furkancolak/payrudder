-- Phase 35: prepaid credits. A tenant sells credit packs; a customer's credits live in lots (one per grant, with an optional expiry) and every change
-- is a row in an append-only ledger. A balance is the sum of the lots that have not expired; consuming takes from the lot that expires first.
-- +goose Up
CREATE TABLE credit_packs (
  tenant_id    uuid NOT NULL,
  id           uuid NOT NULL,
  name         text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  credits      bigint NOT NULL CHECK (credits > 0),
  price_minor  bigint NOT NULL CHECK (price_minor > 0),
  currency     char(3) NOT NULL,
  active       boolean NOT NULL DEFAULT true,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id)
);

CREATE TABLE credit_lots (
  tenant_id    uuid NOT NULL,
  id           uuid NOT NULL,
  customer_id  uuid NOT NULL,
  granted      bigint NOT NULL CHECK (granted > 0),
  remaining    bigint NOT NULL CHECK (remaining >= 0 AND remaining <= granted),
  source       text NOT NULL CHECK (source IN ('purchase', 'promo', 'manual')),
  source_ref   text,                       -- the payment link or invoice that paid for a purchase; one lot per reference
  expires_at   timestamptz,                -- NULL: never expires
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id)
);
CREATE UNIQUE INDEX one_lot_per_source_ref ON credit_lots (tenant_id, source, source_ref) WHERE source_ref IS NOT NULL;
-- The lots a consume walks, in the order it takes from them.
CREATE INDEX credit_lots_spendable ON credit_lots (tenant_id, customer_id, expires_at NULLS LAST, created_at) WHERE remaining > 0;

CREATE TABLE credit_ledger (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  customer_id      uuid NOT NULL,
  lot_id           uuid NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('grant', 'consume', 'expire')),
  amount           bigint NOT NULL CHECK (amount <> 0),   -- positive for a grant, negative for consume and expire
  idempotency_key  text,                                  -- a consume or grant the caller may repeat; the repeat finds the first
  reason           text NOT NULL DEFAULT '',
  created_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, lot_id) REFERENCES credit_lots (tenant_id, id),
  CHECK ((kind = 'grant') = (amount > 0))
);
CREATE INDEX credit_ledger_by_customer ON credit_ledger (tenant_id, customer_id, created_at DESC, id);
CREATE INDEX credit_ledger_by_key ON credit_ledger (tenant_id, customer_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

ALTER TABLE credit_packs ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_packs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_packs USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
ALTER TABLE credit_lots ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_lots FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_lots USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
ALTER TABLE credit_ledger ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_ledger FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_ledger USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

GRANT SELECT, INSERT, UPDATE ON credit_packs, credit_lots TO billing_app;
GRANT SELECT, INSERT ON credit_ledger TO billing_app;
GRANT SELECT, INSERT, UPDATE ON credit_lots TO billing_system;
GRANT SELECT, INSERT ON credit_ledger TO billing_system;
GRANT SELECT ON credit_packs TO billing_system;

-- +goose Down
DROP TABLE credit_ledger;
DROP TABLE credit_lots;
DROP TABLE credit_packs;
