-- billing-svc-status 17 (docs/code-quality, plan 05): when a trial ends inside a billing period, the first paid period's fixed fee is charged from the trial's end to the
-- period's end (prorate_after_trial) instead of in full. It is a property of the plan version, so a version keeps the rule it was published under: every version that
-- exists now is false, which is how it has always been billed, and an invoice already written is never touched. New versions default to true (domain.ValidatePlanVersion).

-- +goose Up
ALTER TABLE plan_versions ADD COLUMN prorate_after_trial boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE plan_versions DROP COLUMN prorate_after_trial;
