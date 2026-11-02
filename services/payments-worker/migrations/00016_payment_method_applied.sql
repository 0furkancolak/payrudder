-- The time of the newest saved-payment-method notification applied to a customer (docs/plans/22 payments review, item (f)): a notification the provider repeats after a newer one
-- was applied is older than this and is ignored, so the older card does not come back. Keyed by the customer's external id, like SetCustomerPaymentMethod. Time only: no card data.
-- Like the other tables of payments_db this holds ids and times only and has no row-level security (only payments_system touches it).
-- +goose Up

CREATE TABLE payment_method_applied (
  tenant_id    uuid NOT NULL,
  provider     text NOT NULL,
  environment  text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  customer_ref text NOT NULL,
  occurred_at  timestamptz NOT NULL,
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, provider, environment, customer_ref)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON payment_method_applied TO payments_system;

-- +goose Down
DROP TABLE payment_method_applied;
