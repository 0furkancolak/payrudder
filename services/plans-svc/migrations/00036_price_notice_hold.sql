-- Plan 15: a notice mail held until N days before the new price. The price change keeps how many days before the new price its notices go out (null: at once, when the run
-- schedules the change). A scheduled item whose mail is held carries the fact to send (notice_event, a PriceChangeScheduled) and the time it is due (notice_send_at);
-- notified_at stays null until a sweep has sent it. Sending is one UPDATE ... WHERE notified_at IS NULL in the same transaction as the outbox row, so it happens once.

-- +goose Up
ALTER TABLE price_changes ADD COLUMN notice_send_days_before integer CHECK (notice_send_days_before BETWEEN 14 AND 365);
ALTER TABLE price_change_items ADD COLUMN notice_send_at timestamptz;
ALTER TABLE price_change_items ADD COLUMN notice_event bytea;
ALTER TABLE price_change_items ADD CONSTRAINT price_change_items_held_notice CHECK ((notice_send_at IS NULL) = (notice_event IS NULL));
CREATE INDEX price_change_items_notice_due ON price_change_items (notice_send_at) WHERE notified_at IS NULL AND notice_event IS NOT NULL;

-- +goose Down
DROP INDEX price_change_items_notice_due;
ALTER TABLE price_change_items DROP CONSTRAINT price_change_items_held_notice;
ALTER TABLE price_change_items DROP COLUMN notice_event;
ALTER TABLE price_change_items DROP COLUMN notice_send_at;
ALTER TABLE price_changes DROP COLUMN notice_send_days_before;
