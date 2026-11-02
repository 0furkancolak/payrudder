-- Plan 15, leftovers. (1) The date model: a price change may name the date its new price starts from (effective_from); each subscription then moves on its first period
-- start on or after that date, instead of on its next renewal. Null keeps the old model. (2) The notice a customer must get: a table of the least notice per country
-- and the notice a run uses when its request names none. Both are platform data the operator edits, not tenant data (no tenant_id, so no twin in tenant/): a tenant in a
-- cell reads them through the system pool, like the kill switch of 00020.
-- The rows are NOT legal advice and UNVERIFIED: nobody with the right to say what a country requires has read them (docs/plans/15-price-increase.md). The one seeded row
-- is the code's own floor of 14 days for every country.

-- +goose Up
ALTER TABLE price_changes ADD COLUMN effective_from timestamptz;

CREATE TABLE price_change_notice_rules (
  country         text PRIMARY KEY CHECK (country = '*' OR country ~ '^[A-Z]{2}$'),
  min_notice_days integer NOT NULL CHECK (min_notice_days BETWEEN 14 AND 365),
  note            text NOT NULL DEFAULT '' CHECK (length(note) <= 200)
);
INSERT INTO price_change_notice_rules (country, min_notice_days, note) VALUES ('*', 14, 'Default floor of the code (a guess, UNVERIFIED, not legal advice)');

CREATE TABLE price_change_notice_settings (
  singleton           boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  default_notice_days integer NOT NULL DEFAULT 30 CHECK (default_notice_days BETWEEN 14 AND 365),
  updated_by          text NOT NULL DEFAULT '' CHECK (length(updated_by) <= 120),
  updated_at          timestamptz
);
INSERT INTO price_change_notice_settings (singleton) VALUES (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON price_change_notice_rules TO plans_system;
GRANT SELECT, UPDATE ON price_change_notice_settings TO plans_system;

-- +goose Down
DROP TABLE price_change_notice_settings;
DROP TABLE price_change_notice_rules;
ALTER TABLE price_changes DROP COLUMN effective_from;
