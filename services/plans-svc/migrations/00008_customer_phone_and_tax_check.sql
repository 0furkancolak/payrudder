-- A customer's mobile number and when its tax id last passed the local check.
-- phone: international form (E.164, "+905321234567"), normalised by plans-svc before it is stored; empty = none.
-- tax_id_validated_at: set by SetCustomerTax when the id passes the checksum or shape check of pkg/taxid (not a registry lookup); NULL otherwise.

-- +goose Up
ALTER TABLE customers
  ADD COLUMN phone text NOT NULL DEFAULT '' CHECK (phone ~ '^(\+[1-9][0-9]{6,14})?$'),
  ADD COLUMN tax_id_validated_at timestamptz;

-- +goose Down
ALTER TABLE customers DROP COLUMN tax_id_validated_at, DROP COLUMN phone;
