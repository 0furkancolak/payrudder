-- Plan 22 (G10): payrudderctl secrets reseal seals the stored boxes again with a new key (docs/runbooks/secrets-key-rotation.md). It runs as the system role, which reads every
-- tenant's rows (BYPASSRLS) but could only read these two tables: it may now replace the boxes, and nothing else in them.

-- +goose Up
GRANT UPDATE (secret_enc, prev_secret_enc) ON webhook_endpoints TO plans_system;
GRANT UPDATE (api_key_enc) ON tenant_mail_integrations TO plans_system;

-- +goose Down
REVOKE UPDATE (secret_enc, prev_secret_enc) ON webhook_endpoints FROM plans_system;
REVOKE UPDATE (api_key_enc) ON tenant_mail_integrations FROM plans_system;
