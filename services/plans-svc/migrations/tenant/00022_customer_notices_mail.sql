-- Plan 13: the notice to a customer that the seller blocked them, or lifted the block, is a mail category (customer_notices) that is OFF until the tenant switches it on, the
-- one category the other way round. "No row" and "disabled" mean on for every other category, so this one is a flag of its own; the CHECK on disabled is unchanged.

-- +goose Up
ALTER TABLE tenant_mail_settings ADD COLUMN customer_notices_on boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE tenant_mail_settings DROP COLUMN customer_notices_on;
