-- The table was called invoice_links before it became payment_links; its constraints (and the indexes behind the key constraints) kept the old name.
-- +goose Up
ALTER TABLE payment_links RENAME CONSTRAINT invoice_links_pkey TO payment_links_pkey;
ALTER TABLE payment_links RENAME CONSTRAINT invoice_links_token_hash_key TO payment_links_token_hash_key;
ALTER TABLE payment_links RENAME CONSTRAINT invoice_links_token_hash_check TO payment_links_token_hash_check;
ALTER TABLE payment_links RENAME CONSTRAINT invoice_links_tenant_id_invoice_id_fkey TO payment_links_tenant_id_invoice_id_fkey;

-- +goose Down
ALTER TABLE payment_links RENAME CONSTRAINT payment_links_tenant_id_invoice_id_fkey TO invoice_links_tenant_id_invoice_id_fkey;
ALTER TABLE payment_links RENAME CONSTRAINT payment_links_token_hash_check TO invoice_links_token_hash_check;
ALTER TABLE payment_links RENAME CONSTRAINT payment_links_token_hash_key TO invoice_links_token_hash_key;
ALTER TABLE payment_links RENAME CONSTRAINT payment_links_pkey TO invoice_links_pkey;
