-- Phase 23: a hosted invoice page. A tenant makes an unguessable link to an invoice and sends it to its customer; the link shows the invoice and its PDF without a
-- sign-in. Only a hash of the token is stored.
-- +goose Up
CREATE TABLE invoice_links (
  tenant_id   uuid NOT NULL,
  id          uuid NOT NULL,
  invoice_id  uuid NOT NULL,
  token_hash  bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  revoked_at  timestamptz,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);
CREATE INDEX invoice_links_by_invoice ON invoice_links (tenant_id, invoice_id);
ALTER TABLE invoice_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoice_links USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, UPDATE ON invoice_links TO billing_app;
GRANT SELECT ON invoice_links TO billing_system;

-- +goose Down
DROP TABLE invoice_links;
