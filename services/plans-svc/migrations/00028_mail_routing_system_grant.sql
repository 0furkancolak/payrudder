-- Several e-mail accounts (00027): the rules that send a language through an account are read by the system role too, as the other mail tables are (the
-- dispatcher and the notifier's reads across tenants). Without this a mail failed with "permission denied for table tenant_mail_routing".

-- +goose Up
GRANT SELECT ON tenant_mail_routing TO plans_system;

-- +goose Down
REVOKE SELECT ON tenant_mail_routing FROM plans_system;
