-- Plan 05: the tenant's default language. The language of a customer's invoice PDF and mails is the customer's own (customers.locale); a customer with none gets this one. It
-- replaces a fixed map from the seller's country to a language that billing-svc kept in code (TR to tr, DE and AT to de, FR to fr, ES to es, anything else English), so a seller
-- can say what it wants. Empty is English. Every tenant that exists gets what that map gave it, so no tenant's documents change language until its owner chooses.
-- +goose Up
ALTER TABLE tenants ADD COLUMN default_language text NOT NULL DEFAULT '' CHECK (default_language IN ('', 'en', 'tr', 'de', 'fr', 'es', 'ar'));
UPDATE tenants SET default_language = CASE upper(address ->> 'country') WHEN 'TR' THEN 'tr' WHEN 'DE' THEN 'de' WHEN 'AT' THEN 'de' WHEN 'FR' THEN 'fr' WHEN 'ES' THEN 'es' ELSE '' END;

-- +goose Down
ALTER TABLE tenants DROP COLUMN default_language;
