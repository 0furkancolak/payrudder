-- The erasure of the IP address and user agent of contract acceptances, as plans_system (BYPASSRLS), over every tenant of the schema it runs in: each tenant with the
-- retention it chose, then the tenants that never chose, with the default.
-- name: ListContractRetention :many
SELECT tenant_id, retention_months FROM contract_settings;

-- name: EraseContractPersonalDataOf :execrows
UPDATE contract_acceptances SET ip = '', user_agent = ''
WHERE tenant_id = @tenant_id AND accepted_at < @cutoff::timestamptz AND (ip <> '' OR user_agent <> '');

-- name: EraseContractPersonalDataOthers :execrows
UPDATE contract_acceptances SET ip = '', user_agent = ''
WHERE tenant_id <> ALL(@configured::uuid[]) AND accepted_at < @cutoff::timestamptz AND (ip <> '' OR user_agent <> '');
