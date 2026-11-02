-- Disputes and chargebacks (docs/plans/22): whether a formal dispute of a customer's payment blocks the customer (category chargeback). The seller decides; off by default.
-- It sits with the dunning settings, the seller's policy for payments that go wrong, and is read by RecordPaymentDispute.
-- +goose Up
ALTER TABLE dunning_settings ADD COLUMN block_on_chargeback boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE dunning_settings DROP COLUMN block_on_chargeback;
