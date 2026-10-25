-- Phase 47, part 2: what an affiliate earns.
--
-- One commission per paid invoice of an attributed customer (invoice_id is unique, so replaying invoice.paid creates one). It waits as `pending` for the program's
-- hold, the refund window, then a job makes it `approved`; a payout makes it `paid` (00049). A refund, void, credit note or chargeback takes the commission back:
-- before it is paid that is a number on the commission (reversed_minor; `reversed` once all of it is gone); after it is paid it is a negative row of its own,
-- approved at once, that the next payout nets. Every reversal is recorded once under its key, so a repeated credit-note event takes back nothing twice.
-- +goose Up
CREATE TABLE affiliate_commissions (
  tenant_id           uuid NOT NULL,
  id                  uuid NOT NULL DEFAULT gen_random_uuid(),
  affiliate_id        uuid NOT NULL,
  customer_id         uuid NOT NULL,
  kind                text NOT NULL DEFAULT 'commission' CHECK (kind IN ('commission', 'reversal')),
  invoice_id          uuid NOT NULL,
  invoice_number      text NOT NULL DEFAULT '',
  currency            char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  -- What the invoice came to, tax included, and the base the commission was taken from: the net, pre-tax amount actually paid.
  invoice_total_minor bigint NOT NULL CHECK (invoice_total_minor >= 0),
  net_minor           bigint NOT NULL CHECK (net_minor >= 0),
  -- The commission, in the invoice's currency; negative on a reversal row.
  amount_minor        bigint NOT NULL CHECK (amount_minor <> 0),
  -- How much of a commission has been taken back so far (a reversal row records its own amount instead).
  reversed_minor      bigint NOT NULL DEFAULT 0 CHECK (reversed_minor >= 0),
  status              text NOT NULL CHECK (status IN ('pending', 'approved', 'paid', 'reversed')),
  parent_id           uuid,
  reversal_key        text,
  earned_at           timestamptz NOT NULL,
  approve_after       timestamptz NOT NULL,
  approved_at         timestamptz,
  paid_at             timestamptz,
  payout_id           uuid,
  created_at          timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
  FOREIGN KEY (tenant_id, parent_id) REFERENCES affiliate_commissions (tenant_id, id),
  CONSTRAINT commission_shape CHECK (
    (kind = 'commission' AND amount_minor > 0 AND parent_id IS NULL AND reversal_key IS NULL AND reversed_minor <= amount_minor)
    OR (kind = 'reversal' AND amount_minor < 0 AND parent_id IS NOT NULL AND reversal_key IS NOT NULL AND reversed_minor = 0 AND status IN ('approved', 'paid'))
  ),
  CONSTRAINT commission_status_matches CHECK (
    (status = 'paid') = (paid_at IS NOT NULL) AND (status <> 'approved' OR approved_at IS NOT NULL)
  )
);
-- Replaying invoice.paid, or two instances handling it at once, makes one commission.
CREATE UNIQUE INDEX affiliate_commissions_invoice ON affiliate_commissions (tenant_id, invoice_id) WHERE kind = 'commission';
CREATE UNIQUE INDEX affiliate_commissions_reversal_key ON affiliate_commissions (tenant_id, reversal_key) WHERE reversal_key IS NOT NULL;
CREATE INDEX affiliate_commissions_by_affiliate ON affiliate_commissions (tenant_id, affiliate_id, status, earned_at DESC, id);
CREATE INDEX affiliate_commissions_by_customer ON affiliate_commissions (tenant_id, customer_id, earned_at);
CREATE INDEX affiliate_commissions_by_date ON affiliate_commissions (tenant_id, earned_at DESC, id);
-- The job that approves commissions finds them by this alone, across tenants.
CREATE INDEX affiliate_commissions_due ON affiliate_commissions (approve_after) WHERE status = 'pending';

CREATE TABLE affiliate_reversals (
  tenant_id       uuid NOT NULL,
  key             text NOT NULL CHECK (length(key) BETWEEN 1 AND 300),
  commission_id   uuid NOT NULL,
  invoice_id      uuid NOT NULL,
  kind            text NOT NULL CHECK (kind IN ('void', 'credit_note', 'chargeback', 'manual')),
  -- For a credit note, what it credited of the invoice (gross); the sum of these is how much of the invoice has been credited back.
  credited_minor  bigint NOT NULL DEFAULT 0 CHECK (credited_minor >= 0),
  -- How much of the commission this event took back.
  amount_minor    bigint NOT NULL CHECK (amount_minor >= 0),
  note            text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  created_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, key),
  FOREIGN KEY (tenant_id, commission_id) REFERENCES affiliate_commissions (tenant_id, id)
);
CREATE INDEX affiliate_reversals_by_commission ON affiliate_reversals (tenant_id, commission_id);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['affiliate_commissions', 'affiliate_reversals'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE ON affiliate_commissions TO plans_app;
GRANT SELECT, INSERT ON affiliate_reversals TO plans_app;
-- The hold job approves what is due in every tenant; a partner's own portal reads their commissions.
GRANT SELECT, UPDATE ON affiliate_commissions TO plans_system;
GRANT SELECT ON affiliate_reversals TO plans_system;

-- +goose Down
DROP TABLE affiliate_reversals;
DROP TABLE affiliate_commissions;
