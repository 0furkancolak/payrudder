-- A payment account is named by its key: the provider's name, or the provider's name, a tilde and a label ("paddle~eu"), and the key is what a routing rule names, what a
-- subscription sold through the account records and what a provider migration moves to (docs/adr/0026, plan 10). The three columns that hold it were written before
-- labels and refused the tilde, so a rule naming a labelled account could not be saved and a subscription could not be sold through one. The shape is the registry's:
-- a provider name, optionally followed by a label of lower-case letters, digits and dashes (pkg/payments/accountkey.go).

-- +goose Up
ALTER TABLE routing_rules DROP CONSTRAINT routing_rules_provider_check;
ALTER TABLE routing_rules ADD CONSTRAINT routing_rules_provider_check CHECK (provider ~ '^[a-z][a-z0-9_-]{0,31}(~[a-z0-9][a-z0-9-]{0,30})?$');
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_provider_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_provider_check CHECK (provider ~ '^[a-z][a-z0-9_-]{0,31}(~[a-z0-9][a-z0-9-]{0,30})?$');
ALTER TABLE provider_migrations DROP CONSTRAINT provider_migrations_to_provider_check;
ALTER TABLE provider_migrations ADD CONSTRAINT provider_migrations_to_provider_check CHECK (to_provider ~ '^[a-z][a-z0-9_-]{0,31}(~[a-z0-9][a-z0-9-]{0,30})?$');

-- +goose Down
-- Going back fails if a labelled account is in use, which is what the narrower rule would have refused.
ALTER TABLE provider_migrations DROP CONSTRAINT provider_migrations_to_provider_check;
ALTER TABLE provider_migrations ADD CONSTRAINT provider_migrations_to_provider_check CHECK (to_provider ~ '^[a-z][a-z0-9_-]{0,31}$');
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_provider_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_provider_check CHECK (provider ~ '^[a-z][a-z0-9_-]{0,31}$');
ALTER TABLE routing_rules DROP CONSTRAINT routing_rules_provider_check;
ALTER TABLE routing_rules ADD CONSTRAINT routing_rules_provider_check CHECK (provider ~ '^[a-z][a-z0-9_-]{0,31}$');
