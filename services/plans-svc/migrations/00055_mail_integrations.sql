-- Phase 53: a live tenant can send its customer mail through its own e-mail provider (Nafru Email, which speaks the Resend API) instead of Payrudder's
-- built-in sender. One row per tenant. The API key is sealed (pkg/secretbox, the tenant id as associated data); only the last four characters are kept in the
-- clear, to tell which key is stored. The notifier reads the connection through plans-svc (ResolveMailSender) before it sends.

-- +goose Up
CREATE TABLE tenant_mail_integrations (
  tenant_id      uuid PRIMARY KEY,
  provider       text NOT NULL CHECK (provider IN ('nafru')),
  base_url       text NOT NULL CHECK (base_url <> ''),
  api_key_enc    bytea NOT NULL,
  key_hint       text NOT NULL DEFAULT '',
  from_address   text NOT NULL CHECK (from_address <> ''),
  checked_at     timestamptz,
  check_problem  text NOT NULL DEFAULT '' CHECK (check_problem IN ('', 'unreachable', 'refused')),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE tenant_mail_integrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_mail_integrations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_mail_integrations USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_mail_integrations TO plans_app;
GRANT SELECT ON tenant_mail_integrations TO plans_system;

-- +goose Down
DROP TABLE tenant_mail_integrations;
