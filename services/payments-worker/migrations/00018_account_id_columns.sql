-- Step 4 of the account_id migration (ADR 0026, docs/runbooks/account-id-migration-rehearsal.md): the ledger and its neighbours get a nullable account_id beside the key. Nothing
-- reads it yet and no key column changes. ADD COLUMN without a default is a catalog change (no rewrite, no long lock); the existing rows are filled in batches by
-- `payrudderctl account-ids`, not here. DEFAULTS (not agreed): no foreign key to integration_accounts (it would check every backfilled row and block the account's removal; the
-- batch job's verify mode checks the link instead), no index on account_id until a reader needs one (step 5), the trigger fills only a NULL.

-- +goose Up
-- +goose StatementBegin
-- The id of the payment account a (tenant, environment, key) names, or NULL when no account has been made for it yet (step 2 not run, or an account created since).
-- The key is "provider" or "provider~label" (pkg/payments.AccountKey): the label is what follows the first "~".
CREATE FUNCTION payment_account_id(t uuid, env text, key text) RETURNS uuid
LANGUAGE sql STABLE AS $$
  SELECT id FROM integration_accounts
   WHERE owner_kind = 'tenant' AND owner_id = t AND kind = 'payment' AND environment = env
     AND provider = split_part(key, '~', 1) AND label = substr(key, length(split_part(key, '~', 1)) + 2)
$$;

-- Every writer of these tables keeps writing the key, as before; a row inserted without an account_id gets the id of the account the key names, so no writer had to change
-- and a code rollback leaves nothing behind. A key with no account yet stays NULL for the batch backfill (payrudderctl account-ids) to fill.
CREATE FUNCTION fill_account_id() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.account_id := payment_account_id(NEW.tenant_id, NEW.environment, NEW.provider);
  RETURN NEW;
END $$;
-- +goose StatementEnd

ALTER TABLE payment_ledger ADD COLUMN account_id uuid;
ALTER TABLE provider_objects ADD COLUMN account_id uuid;
ALTER TABLE payment_attempts ADD COLUMN account_id uuid;
ALTER TABLE provider_subscriptions ADD COLUMN account_id uuid;
ALTER TABLE reconciliation_discrepancies ADD COLUMN account_id uuid;
ALTER TABLE settlement_lines ADD COLUMN account_id uuid;
ALTER TABLE fee_rules ADD COLUMN account_id uuid;
ALTER TABLE provider_account_health ADD COLUMN account_id uuid;

-- The ledger and provider_objects are immutable by grants (payments_system has no UPDATE on them), and three tables allow UPDATE of named columns only. The backfill needs this one column; no amount, status or key can be changed.
GRANT UPDATE (account_id) ON payment_ledger, provider_objects, reconciliation_discrepancies, settlement_lines, fee_rules TO payments_system;

CREATE TRIGGER payment_ledger_account_id BEFORE INSERT ON payment_ledger FOR EACH ROW WHEN (NEW.account_id IS NULL AND NEW.provider <> '') EXECUTE FUNCTION fill_account_id();
CREATE TRIGGER provider_objects_account_id BEFORE INSERT ON provider_objects FOR EACH ROW WHEN (NEW.account_id IS NULL AND NEW.provider <> '') EXECUTE FUNCTION fill_account_id();
CREATE TRIGGER payment_attempts_account_id BEFORE INSERT ON payment_attempts FOR EACH ROW WHEN (NEW.account_id IS NULL AND NEW.provider <> '') EXECUTE FUNCTION fill_account_id();
CREATE TRIGGER provider_subscriptions_account_id BEFORE INSERT ON provider_subscriptions FOR EACH ROW WHEN (NEW.account_id IS NULL AND NEW.provider <> '') EXECUTE FUNCTION fill_account_id();
CREATE TRIGGER reconciliation_discrepancies_account_id BEFORE INSERT ON reconciliation_discrepancies FOR EACH ROW WHEN (NEW.account_id IS NULL AND NEW.provider <> '') EXECUTE FUNCTION fill_account_id();
CREATE TRIGGER settlement_lines_account_id BEFORE INSERT ON settlement_lines FOR EACH ROW WHEN (NEW.account_id IS NULL AND NEW.provider <> '') EXECUTE FUNCTION fill_account_id();
CREATE TRIGGER fee_rules_account_id BEFORE INSERT ON fee_rules FOR EACH ROW WHEN (NEW.account_id IS NULL AND NEW.provider <> '') EXECUTE FUNCTION fill_account_id();
CREATE TRIGGER provider_account_health_account_id BEFORE INSERT ON provider_account_health FOR EACH ROW WHEN (NEW.account_id IS NULL AND NEW.provider <> '') EXECUTE FUNCTION fill_account_id();

-- +goose Down
DROP TRIGGER payment_ledger_account_id ON payment_ledger;
DROP TRIGGER provider_objects_account_id ON provider_objects;
DROP TRIGGER payment_attempts_account_id ON payment_attempts;
DROP TRIGGER provider_subscriptions_account_id ON provider_subscriptions;
DROP TRIGGER reconciliation_discrepancies_account_id ON reconciliation_discrepancies;
DROP TRIGGER settlement_lines_account_id ON settlement_lines;
DROP TRIGGER fee_rules_account_id ON fee_rules;
DROP TRIGGER provider_account_health_account_id ON provider_account_health;
DROP FUNCTION fill_account_id();
DROP FUNCTION payment_account_id(uuid, text, text);
ALTER TABLE payment_ledger DROP COLUMN account_id;
ALTER TABLE provider_objects DROP COLUMN account_id;
ALTER TABLE payment_attempts DROP COLUMN account_id;
ALTER TABLE provider_subscriptions DROP COLUMN account_id;
ALTER TABLE reconciliation_discrepancies DROP COLUMN account_id;
ALTER TABLE settlement_lines DROP COLUMN account_id;
ALTER TABLE fee_rules DROP COLUMN account_id;
ALTER TABLE provider_account_health DROP COLUMN account_id;
