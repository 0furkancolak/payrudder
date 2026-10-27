-- A subscription has a quantity (seats, licences): a plain multiplier of the plan's fixed fee. Usage prices are not multiplied. A scheduled change may carry the
-- quantity it moves to; NULL keeps the subscription's own.

-- +goose Up
ALTER TABLE subscriptions ADD COLUMN quantity integer NOT NULL DEFAULT 1 CHECK (quantity BETWEEN 1 AND 100000);
ALTER TABLE subscription_changes ADD COLUMN quantity integer CHECK (quantity BETWEEN 1 AND 100000);

-- +goose Down
ALTER TABLE subscription_changes DROP COLUMN quantity;
ALTER TABLE subscriptions DROP COLUMN quantity;
