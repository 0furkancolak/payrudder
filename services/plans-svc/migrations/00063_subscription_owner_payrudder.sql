-- Who renews a subscription and retries its failed renewals was `quotify` (the product's old name) or `provider`; it is `payrudder` or `provider`. Nothing is released,
-- so the stored rows are rewritten and the check constraints and the column default change with them.

-- +goose Up
-- The migrator owns the table and RLS is forced on it, so its statements see no rows (there is no tenant). The rewrite needs every tenant's rows: the owner is
-- exempt while FORCE is off, and it is switched on again before the migration ends.
ALTER TABLE subscriptions NO FORCE ROW LEVEL SECURITY;
ALTER TABLE subscriptions DROP CONSTRAINT provider_owned_has_provider;
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_owner_check;
UPDATE subscriptions SET owner = 'payrudder' WHERE owner = 'quotify';
ALTER TABLE subscriptions ALTER COLUMN owner SET DEFAULT 'payrudder';
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_owner_check CHECK (owner IN ('payrudder', 'provider'));
ALTER TABLE subscriptions ADD CONSTRAINT provider_owned_has_provider CHECK (owner = 'payrudder' OR (provider IS NOT NULL AND provider_ref IS NOT NULL));
ALTER TABLE subscriptions FORCE ROW LEVEL SECURITY;

-- +goose Down
ALTER TABLE subscriptions NO FORCE ROW LEVEL SECURITY;
ALTER TABLE subscriptions DROP CONSTRAINT provider_owned_has_provider;
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_owner_check;
UPDATE subscriptions SET owner = 'quotify' WHERE owner = 'payrudder';
ALTER TABLE subscriptions ALTER COLUMN owner SET DEFAULT 'quotify';
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_owner_check CHECK (owner IN ('quotify', 'provider'));
ALTER TABLE subscriptions ADD CONSTRAINT provider_owned_has_provider CHECK (owner = 'quotify' OR (provider IS NOT NULL AND provider_ref IS NOT NULL));
ALTER TABLE subscriptions FORCE ROW LEVEL SECURITY;
