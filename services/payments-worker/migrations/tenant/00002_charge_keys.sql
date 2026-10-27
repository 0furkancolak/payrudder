-- payments_db, tenant schema: the index that finds the tenant of a provider notification (phase 28, ADR 0018).
--
-- A notification of a provider whose URL names no tenant (iyzico) carries only the idempotency key of the charge it is about, and that key says whose charge it was
-- only by being looked up. In the pooled database that is one index scan of payment_attempts. In a cell the charge is in the schema of its tenant, and looking at
-- every schema is not an option, so each cell database keeps shared.charge_keys: key -> tenant, written by a trigger on payment_attempts in the same transaction as
-- the attempt. A trigger, not application code, so that it also fires when a move copies a tenant's attempts into a cell (pgxx.Move inserts into the real tables).
--
-- shared is the one schema of a cell the tenant schemas have in common (created before any tenant schema, with USAGE for the roles), and every tenant's migration
-- creates what it needs of it if it is not there yet; the lock makes the first ones, running at once for different tenants, take turns. An entry says only which
-- tenant a key belongs to: where that tenant lives is the directory's answer, so an entry left behind by a move still names the right tenant and is harmless.
-- The roles' grants here are exactly the two things the service does with it: write through the trigger (and delete on a sandbox reset) and read.

-- +goose Up
SELECT pg_advisory_xact_lock(hashtextextended('payrudder payments shared.charge_keys', 0));

CREATE TABLE IF NOT EXISTS shared.charge_keys (
  idempotency_key  text PRIMARY KEY,
  tenant_id        uuid NOT NULL,
  indexed_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS charge_keys_by_tenant ON shared.charge_keys (tenant_id);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION shared.index_charge_key() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  -- The newest charge under a key wins, as the pooled lookup (the most recent attempt) has it.
  INSERT INTO shared.charge_keys (idempotency_key, tenant_id) VALUES (NEW.idempotency_key, NEW.tenant_id)
  ON CONFLICT (idempotency_key) DO UPDATE SET tenant_id = EXCLUDED.tenant_id, indexed_at = now();
  RETURN NULL;
END
$$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE, DELETE ON shared.charge_keys TO payments_system;
GRANT SELECT ON shared.charge_keys TO payments_app;

CREATE TRIGGER payment_attempts_index_charge_key AFTER INSERT ON payment_attempts
FOR EACH ROW WHEN (NEW.kind = 'charge') EXECUTE FUNCTION shared.index_charge_key();

-- +goose Down
DROP TRIGGER payment_attempts_index_charge_key ON payment_attempts;
