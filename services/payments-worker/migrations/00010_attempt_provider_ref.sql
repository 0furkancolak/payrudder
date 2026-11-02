-- The provider's own reference of a charge, kept with the attempt that got it (docs/plans/08, plan 22): a Stripe PaymentIntent id, from the first response of the charge, be it
-- a success, a decline, a payment that is processing or one that waits for the customer. A status query asks the provider by it (payments.RefStatuser) instead of searching by
-- the key, which Stripe's search cannot answer for up to a minute after the charge. NULL while the provider has not answered, or for a provider with no such reference.
-- +goose Up
ALTER TABLE payment_attempts ADD COLUMN provider_ref text;

-- +goose Down
ALTER TABLE payment_attempts DROP COLUMN provider_ref;
