-- Phase 53: an API key has a role and the permissions it was issued with, instead of the three scopes (full, ingest, read).
--
-- The role names a set (admin, billing, read_only, ingest_only) or says the permissions were chosen one by one (custom). The permissions are what the
-- gateway enforces, resource:action, from the vocabulary in pkg/apikey. Existing keys keep what they could do: full -> admin (every permission), ingest ->
-- ingest_only (events:write), read -> read_only (every :read). The list below is the vocabulary at the time of writing; a permission added later reaches the
-- admin keys that already exist through the gateway, which treats the role admin as holding every permission.

-- +goose Up
ALTER TABLE api_keys
  ADD COLUMN role        text   NOT NULL DEFAULT 'admin' CHECK (role IN ('admin', 'billing', 'read_only', 'ingest_only', 'custom')),
  ADD COLUMN permissions text[] NOT NULL DEFAULT '{}';

UPDATE api_keys SET role = 'ingest_only' WHERE scope = 'ingest';
UPDATE api_keys SET role = 'read_only'   WHERE scope = 'read';

UPDATE api_keys SET permissions = ARRAY[
  'events:write', 'customers:read', 'customers:write', 'subscriptions:read', 'subscriptions:write', 'catalog:read', 'catalog:write', 'usage:read',
  'credits:read', 'credits:write', 'invoices:read', 'invoices:write', 'reports:read', 'accounting:read', 'accounting:write', 'webhooks:read', 'webhooks:write',
  'affiliates:read', 'affiliates:write', 'settings:read', 'settings:write', 'assets:read', 'assets:write', 'sandbox:write'] WHERE role = 'admin';
UPDATE api_keys SET permissions = ARRAY['events:write'] WHERE role = 'ingest_only';
UPDATE api_keys SET permissions = ARRAY[
  'customers:read', 'subscriptions:read', 'catalog:read', 'usage:read', 'credits:read', 'invoices:read', 'reports:read', 'accounting:read', 'webhooks:read',
  'affiliates:read', 'settings:read', 'assets:read'] WHERE role = 'read_only';

ALTER TABLE api_keys
  DROP COLUMN scope,
  ADD CONSTRAINT api_keys_permissions_present CHECK (role = 'admin' OR cardinality(permissions) > 0);

-- +goose Down
ALTER TABLE api_keys
  DROP CONSTRAINT api_keys_permissions_present,
  ADD COLUMN scope text NOT NULL DEFAULT 'full' CHECK (scope IN ('full', 'ingest', 'read'));
UPDATE api_keys SET scope = 'ingest' WHERE role = 'ingest_only';
UPDATE api_keys SET scope = 'read'   WHERE role IN ('read_only', 'billing', 'custom');
ALTER TABLE api_keys DROP COLUMN permissions, DROP COLUMN role;
