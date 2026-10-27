-- Two more meter aggregations: UNIQUE (the number of distinct values of value_property per period) and LAST (the quantity of the latest event of the period).
-- Only the list in the CHECK grows; the rule that value_property is empty exactly for COUNT already fits both (they name a property).

-- +goose Up
ALTER TABLE meters DROP CONSTRAINT meters_aggregation_check;
ALTER TABLE meters ADD CONSTRAINT meters_aggregation_check CHECK (aggregation IN ('SUM', 'COUNT', 'MAX', 'UNIQUE', 'LAST'));

-- +goose Down
-- A meter version that is UNIQUE or LAST has no narrower equivalent, so the narrower check fails while any exists: retire them first.
ALTER TABLE meters DROP CONSTRAINT meters_aggregation_check;
ALTER TABLE meters ADD CONSTRAINT meters_aggregation_check CHECK (aggregation IN ('SUM', 'COUNT', 'MAX'));
