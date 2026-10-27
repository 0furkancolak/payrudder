-- Quota windows grow by two: a calendar year (twelve billing periods) and the customer's lifetime. Both are only a wider list in the CHECK: no row changes, and a
-- row that says 'month' (the default) still means the billing period.

-- +goose Up
ALTER TABLE plan_entitlements DROP CONSTRAINT plan_entitlements_quota_window_check;
ALTER TABLE plan_entitlements ADD CONSTRAINT plan_entitlements_quota_window_check
  CHECK (quota_window IN ('hour', 'day', 'week', 'month', 'year', 'lifetime'));
ALTER TABLE customer_entitlement_overrides DROP CONSTRAINT customer_entitlement_overrides_quota_window_check;
ALTER TABLE customer_entitlement_overrides ADD CONSTRAINT customer_entitlement_overrides_quota_window_check
  CHECK (quota_window IN ('hour', 'day', 'week', 'month', 'year', 'lifetime'));

-- +goose Down
-- A row with a year or lifetime window has no shorter equivalent: it becomes the month before the narrower check is restored.
UPDATE plan_entitlements SET quota_window = 'month' WHERE quota_window IN ('year', 'lifetime');
UPDATE customer_entitlement_overrides SET quota_window = 'month' WHERE quota_window IN ('year', 'lifetime');
ALTER TABLE customer_entitlement_overrides DROP CONSTRAINT customer_entitlement_overrides_quota_window_check;
ALTER TABLE customer_entitlement_overrides ADD CONSTRAINT customer_entitlement_overrides_quota_window_check
  CHECK (quota_window IN ('hour', 'day', 'week', 'month'));
ALTER TABLE plan_entitlements DROP CONSTRAINT plan_entitlements_quota_window_check;
ALTER TABLE plan_entitlements ADD CONSTRAINT plan_entitlements_quota_window_check
  CHECK (quota_window IN ('hour', 'day', 'week', 'month'));
