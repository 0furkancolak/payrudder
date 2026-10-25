-- name: ClearDefaultProviderAccounts :exec
UPDATE provider_accounts SET is_default = false
WHERE tenant_id = @tenant_id AND environment = @environment AND is_default AND provider <> @provider;

-- name: UpsertProviderAccount :exec
INSERT INTO provider_accounts (tenant_id, provider, environment, is_default, settings_enc)
VALUES (@tenant_id, @provider, @environment, @is_default, @settings_enc)
ON CONFLICT (tenant_id, provider, environment) DO UPDATE
SET settings_enc = EXCLUDED.settings_enc, is_default = provider_accounts.is_default OR EXCLUDED.is_default, updated_at = now();

-- name: DeleteProviderAccount :execrows
DELETE FROM provider_accounts WHERE tenant_id = @tenant_id AND provider = @provider AND environment = @environment;

-- name: ListProviderAccounts :many
SELECT provider, environment, is_default, updated_at FROM provider_accounts WHERE tenant_id = @tenant_id ORDER BY environment, provider;

-- name: GetProviderAccountSettings :one
SELECT settings_enc, updated_at FROM provider_accounts WHERE tenant_id = @tenant_id AND provider = @provider AND environment = @environment;

-- name: GetDefaultProvider :one
SELECT provider FROM provider_accounts WHERE tenant_id = @tenant_id AND environment = @environment AND is_default;

-- name: InsertProviderAccountEvent :exec
INSERT INTO provider_account_events (tenant_id, provider, environment, action, actor, is_default, verified_remote)
VALUES (@tenant_id, @provider, @environment, @action, @actor, @is_default, @verified_remote);
