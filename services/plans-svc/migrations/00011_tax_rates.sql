-- Phase 22: the tax rate Quotify adds to the invoices it raises, by the customer's country. The tenant sets the rates and is responsible for them; Quotify applies
-- them as given (exclusive of the price) and does not work out what is due where. An empty country is the rate for every country without its own.

-- +goose Up
CREATE TABLE tax_rates (
  tenant_id uuid NOT NULL,
  country   text NOT NULL CHECK (country = '' OR country ~ '^[A-Z]{2}$'),
  name      text NOT NULL CHECK (length(name) BETWEEN 1 AND 40),
  rate_bps  int  NOT NULL CHECK (rate_bps BETWEEN 0 AND 10000),   -- 2000 = 20 %
  PRIMARY KEY (tenant_id, country)
);
ALTER TABLE tax_rates ENABLE ROW LEVEL SECURITY;
ALTER TABLE tax_rates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tax_rates USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT, DELETE ON tax_rates TO plans_app;
GRANT SELECT ON tax_rates TO plans_system;

-- +goose Down
DROP TABLE tax_rates;
