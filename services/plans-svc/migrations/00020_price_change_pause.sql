-- Plan 15: the kill switch of price changes. One row for the whole platform: while paused is true no run schedules a subscription and the notifier holds the mail of the
-- price changes already scheduled. Not tenant data (no tenant_id), so it has no twin in tenant/ and a tenant in a cell cannot see it: the system pool reads and writes it.

-- +goose Up
CREATE TABLE price_change_pause (
  singleton  boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  paused     boolean NOT NULL DEFAULT false,
  reason     text NOT NULL DEFAULT '' CHECK (length(reason) <= 500),
  updated_by text NOT NULL DEFAULT '' CHECK (length(updated_by) <= 120),
  updated_at timestamptz
);
INSERT INTO price_change_pause (singleton) VALUES (true);
GRANT SELECT, UPDATE ON price_change_pause TO plans_system;

-- +goose Down
DROP TABLE price_change_pause;
