-- Contracts (docs/plans/21-contracts.md): the agreements a payment link asks its payer to accept. Default (not agreed): the link's list replaces the seller's tenant-wide
-- required_kinds for the page of that link, and an empty list means the setting applies. The child invoice link an ad-hoc link hands out copies its parent's list.
-- +goose Up
ALTER TABLE payment_links ADD COLUMN required_contracts text[] NOT NULL DEFAULT '{}'
  CONSTRAINT payment_links_required_contracts_kinds CHECK (required_contracts <@ ARRAY['distance_sales', 'pre_info', 'tos', 'privacy']::text[]);

-- +goose Down
ALTER TABLE payment_links DROP COLUMN required_contracts;
