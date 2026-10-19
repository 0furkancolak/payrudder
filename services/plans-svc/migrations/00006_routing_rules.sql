-- Phase 16: which payment provider a new subscription goes to, chosen from the customer's country and the subscription's currency.
-- +goose Up
CREATE TABLE routing_rules (
  tenant_id  uuid NOT NULL,
  position   int  NOT NULL CHECK (position >= 0),
  country    char(2) CHECK (country ~ '^[A-Z]{2}$'),
  currency   char(3) CHECK (currency ~ '^[A-Z]{3}$'),
  provider   text NOT NULL CHECK (provider ~ '^[a-z][a-z0-9_-]{0,31}$'),
  PRIMARY KEY (tenant_id, position)
);
ALTER TABLE routing_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE routing_rules FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON routing_rules USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, DELETE ON routing_rules TO plans_app;
GRANT SELECT ON routing_rules TO plans_system;

-- +goose Down
DROP TABLE routing_rules;
