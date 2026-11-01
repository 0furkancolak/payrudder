-- An idempotency key is used once per lot (docs/plans/22 G13). A consume that takes from several lots writes one ledger row per lot under the caller's key, and a grant
-- writes one row for its new lot, so the key alone is not unique: (customer, key, lot) is. The index replaces credit_ledger_by_key, which it serves as a prefix.
--
-- Rows that already repeat a (customer, key, lot) would stop the index from being built, so the later ones are renamed first: the earliest keeps its key, a later one gets
-- '#dup-<id>' appended and nothing else about it changes. The owner is under forced row-level security like everyone else, so the policy is set aside for that one
-- statement. The code cannot produce such rows (a customer's credits change one at a time under an advisory lock, after a look at the key); this keeps a migration
-- from failing on data that an earlier bug may have left.
-- +goose Up
ALTER TABLE credit_ledger NO FORCE ROW LEVEL SECURITY;
UPDATE credit_ledger l SET idempotency_key = l.idempotency_key || '#dup-' || l.id::text
FROM (
  SELECT tenant_id, id, row_number() OVER (PARTITION BY tenant_id, customer_id, idempotency_key, lot_id ORDER BY created_at, id) AS n
  FROM credit_ledger WHERE idempotency_key IS NOT NULL
) d
WHERE l.tenant_id = d.tenant_id AND l.id = d.id AND d.n > 1;
ALTER TABLE credit_ledger FORCE ROW LEVEL SECURITY;

DROP INDEX credit_ledger_by_key;
CREATE UNIQUE INDEX credit_ledger_key_once ON credit_ledger (tenant_id, customer_id, idempotency_key, lot_id) WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX credit_ledger_key_once;
CREATE INDEX credit_ledger_by_key ON credit_ledger (tenant_id, customer_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
