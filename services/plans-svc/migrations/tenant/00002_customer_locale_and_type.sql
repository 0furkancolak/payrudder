-- A customer's language and whether it is a person or a business.
-- locale: the language of the mails, receipts and payment pages the customer gets; empty falls back to the browser's language (pages) or English (mails).
-- customer_type: "individual" (a person, buying for themselves) or "business" (a taxpayer, with a tax id and a tax office); empty is unknown, as before.

-- +goose Up
ALTER TABLE customers
  ADD COLUMN locale text NOT NULL DEFAULT '' CHECK (locale IN ('', 'en', 'tr', 'de', 'fr', 'es', 'ar')),
  ADD COLUMN customer_type text NOT NULL DEFAULT '' CHECK (customer_type IN ('', 'individual', 'business'));

-- +goose Down
ALTER TABLE customers DROP COLUMN customer_type, DROP COLUMN locale;
