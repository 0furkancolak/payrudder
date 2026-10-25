-- Phase 47, part 3: paying the affiliates, and the books.
--
-- A payout is a batch of one affiliate's approved commissions in one currency (no conversion, ever): the positive ones and the negative rows that net earlier
-- payments taken back. It is `open` until the owner has paid it (by hand or bank transfer; Payrudder moves no money) and says so; then it is `paid`, its
-- commissions become `paid`, and the journal gets the postings: the commission expense and the payable, then the payable settled against cash. Each posting set
-- balances (debits = credits) in its currency, and the database refuses to commit one that does not.
--
-- A payout of a sandbox tenant is fake: it moves nothing, is marked so, and its postings carry the mark.
--
-- Resetting a sandbox (00049_sandbox_reset) empties the customers' side of it, the payouts and their postings included, since they describe commissions that are gone.
-- The system role may delete them for that, and a trigger lets it only for a sandbox: the journal of a live tenant is never rewritten.
-- +goose Up
CREATE TABLE affiliate_payouts (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL DEFAULT gen_random_uuid(),
  affiliate_id     uuid NOT NULL,
  currency         char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  -- Set when the payout is made, and again when it is paid (what the rows came to then). While it is open the rows say what it is worth now.
  total_minor      bigint NOT NULL,
  commission_count int NOT NULL CHECK (commission_count >= 0),
  method           text NOT NULL CHECK (method IN ('manual', 'bank_transfer')),
  payout_details   text NOT NULL DEFAULT '',
  reference        text NOT NULL DEFAULT '' CHECK (length(reference) <= 200),
  status           text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'paid', 'cancelled')),
  sandbox          boolean NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  paid_at          timestamptz,
  cancelled_at     timestamptz,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates (tenant_id, id),
  CONSTRAINT payout_state CHECK ((status = 'paid') = (paid_at IS NOT NULL) AND (status = 'cancelled') = (cancelled_at IS NOT NULL)),
  CONSTRAINT paid_payout_is_positive CHECK (status <> 'paid' OR total_minor > 0)
);
-- One open payout per affiliate and currency: what is approved later goes in the next one.
CREATE UNIQUE INDEX affiliate_payouts_one_open ON affiliate_payouts (tenant_id, affiliate_id, currency) WHERE status = 'open';
CREATE INDEX affiliate_payouts_by_date ON affiliate_payouts (tenant_id, created_at DESC, id);
CREATE INDEX affiliate_payouts_by_affiliate ON affiliate_payouts (tenant_id, affiliate_id, created_at DESC);

ALTER TABLE affiliate_commissions ADD CONSTRAINT affiliate_commissions_payout_fkey FOREIGN KEY (tenant_id, payout_id) REFERENCES affiliate_payouts (tenant_id, id);
-- What a commission was actually paid out for: its amount less what had been taken back by then (a negative row: its own amount).
ALTER TABLE affiliate_commissions ADD COLUMN settled_minor bigint;
ALTER TABLE affiliate_commissions ADD CONSTRAINT commission_settled_when_paid CHECK ((status = 'paid') = (settled_minor IS NOT NULL));
CREATE INDEX affiliate_commissions_by_payout ON affiliate_commissions (tenant_id, payout_id) WHERE payout_id IS NOT NULL;
-- What the owner can pay: approved and not in a payout yet.
CREATE INDEX affiliate_commissions_payable ON affiliate_commissions (tenant_id, affiliate_id, currency) WHERE status = 'approved' AND payout_id IS NULL;

CREATE TABLE affiliate_journal (
  tenant_id    uuid NOT NULL,
  id           uuid NOT NULL DEFAULT gen_random_uuid(),
  payout_id    uuid NOT NULL,
  affiliate_id uuid NOT NULL,
  posted_at    timestamptz NOT NULL,
  currency     char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  account      text NOT NULL CHECK (account IN ('commission_expense', 'commissions_payable', 'cash')),
  debit_minor  bigint NOT NULL DEFAULT 0 CHECK (debit_minor >= 0),
  credit_minor bigint NOT NULL DEFAULT 0 CHECK (credit_minor >= 0),
  memo         text NOT NULL DEFAULT '',
  sandbox      boolean NOT NULL,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, payout_id) REFERENCES affiliate_payouts (tenant_id, id),
  FOREIGN KEY (tenant_id, affiliate_id) REFERENCES affiliates (tenant_id, id),
  CONSTRAINT one_side CHECK ((debit_minor > 0) <> (credit_minor > 0))
);
CREATE INDEX affiliate_journal_by_payout ON affiliate_journal (tenant_id, payout_id);
CREATE INDEX affiliate_journal_by_date ON affiliate_journal (tenant_id, posted_at DESC, id);

-- The books balance per payout and currency, checked when the transaction commits, so a set of lines may be written in any order.
-- +goose StatementBegin
CREATE FUNCTION affiliate_journal_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  diff bigint;
BEGIN
  SELECT COALESCE(sum(debit_minor), 0) - COALESCE(sum(credit_minor), 0) INTO diff
  FROM affiliate_journal WHERE tenant_id = NEW.tenant_id AND payout_id = NEW.payout_id AND currency = NEW.currency;
  IF diff <> 0 THEN
    RAISE EXCEPTION 'the postings of a payout must balance: debits and credits differ by %', diff USING ERRCODE = 'P0001', HINT = 'journal_unbalanced';
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER affiliate_journal_balanced AFTER INSERT ON affiliate_journal DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION affiliate_journal_balanced();

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['affiliate_payouts', 'affiliate_journal'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- The journal is append-only: no UPDATE, no DELETE for the application role.
GRANT SELECT, INSERT, UPDATE ON affiliate_payouts TO plans_app;
GRANT SELECT, INSERT ON affiliate_journal TO plans_app;
GRANT SELECT ON affiliate_payouts, affiliate_journal TO plans_system;
GRANT DELETE ON affiliate_payouts, affiliate_journal TO plans_system;

-- +goose StatementBegin
CREATE FUNCTION affiliate_journal_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND EXISTS (SELECT 1 FROM tenants WHERE id = OLD.tenant_id AND environment = 'sandbox') THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'the affiliate journal is append-only' USING ERRCODE = 'P0001', HINT = 'journal_append_only';
END $$;
-- +goose StatementEnd
CREATE TRIGGER affiliate_journal_append_only BEFORE UPDATE OR DELETE ON affiliate_journal FOR EACH ROW EXECUTE FUNCTION affiliate_journal_append_only();

-- +goose Down
DROP TABLE affiliate_journal;
DROP FUNCTION affiliate_journal_append_only();
DROP FUNCTION affiliate_journal_balanced();
DROP INDEX affiliate_commissions_payable;
DROP INDEX affiliate_commissions_by_payout;
ALTER TABLE affiliate_commissions DROP CONSTRAINT commission_settled_when_paid, DROP COLUMN settled_minor;
ALTER TABLE affiliate_commissions DROP CONSTRAINT affiliate_commissions_payout_fkey;
DROP TABLE affiliate_payouts;
