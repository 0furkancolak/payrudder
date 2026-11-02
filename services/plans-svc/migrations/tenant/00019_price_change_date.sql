-- Plan 15, leftovers: the date a price change's new price starts from (the twin of ../00027_price_change_date_and_notice.sql; the notice tables there are platform data and have no twin).

-- +goose Up
ALTER TABLE price_changes ADD COLUMN effective_from timestamptz;

-- +goose Down
ALTER TABLE price_changes DROP COLUMN effective_from;
