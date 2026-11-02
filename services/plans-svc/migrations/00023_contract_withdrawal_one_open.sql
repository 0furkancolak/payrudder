-- Contracts (docs/plans/21-contracts.md, security review 9 in docs/plans/22): one open withdrawal from a hosted page for an invoice. The hosted function read "is there a
-- withdrawal for this invoice?" and then inserted, so two presses of the button that raced (two tabs, a double click) both inserted and the customer was acknowledged twice.
-- The database now refuses the second: a unique index over the withdrawals made on a hosted page that name an invoice and are not rejected (a rejected one lets the customer try
-- again). What a seller records through the API or the console is not limited: it is the seller's record of a statement that reached it, not a button.
--
-- Rows that already repeat an invoice would stop the index from being built, so the later undecided ones are rejected first, by 'system' with the reason, and nothing else
-- about them changes: a decided one (accepted, refunded) is never touched and, with the earliest otherwise, keeps the invoice. The owner is under forced row-level security like
-- everyone else, so the policy is set aside for that one statement (as 00004 of billing-svc does). The code cannot make such rows twice; this keeps a migration from failing on the
-- ones the race has made already. If an invoice has more than one decided hosted withdrawal the index cannot be built and the migration fails naming it: that needs a person.

-- +goose Up
ALTER TABLE contract_withdrawals NO FORCE ROW LEVEL SECURITY;
UPDATE contract_withdrawals w SET status = 'rejected', decided_at = COALESCE(w.decided_at, now()), decided_by = 'system',
  decision_note = 'a duplicate of another withdrawal for the same invoice (migration 00022)'
FROM (
  SELECT tenant_id, id, row_number() OVER (PARTITION BY tenant_id, invoice_id ORDER BY (status = 'received'), received_at, id) AS n
  FROM contract_withdrawals WHERE source = 'hosted' AND invoice_id IS NOT NULL AND status <> 'rejected'
) d
WHERE w.tenant_id = d.tenant_id AND w.id = d.id AND d.n > 1 AND w.status = 'received';
ALTER TABLE contract_withdrawals FORCE ROW LEVEL SECURITY;

CREATE UNIQUE INDEX contract_withdrawals_one_open_hosted ON contract_withdrawals (tenant_id, invoice_id)
  WHERE source = 'hosted' AND invoice_id IS NOT NULL AND status <> 'rejected';

-- +goose Down
-- The rows the Up rejected stay rejected.
DROP INDEX contract_withdrawals_one_open_hosted;
