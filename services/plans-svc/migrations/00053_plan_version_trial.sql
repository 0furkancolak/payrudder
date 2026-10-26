-- A plan version can carry a default trial: a subscription created on it without saying otherwise starts trialing for this many days (no card, no fee until
-- the trial ends). 0, the default of every existing version, is no trial.

-- +goose Up
ALTER TABLE plan_versions ADD COLUMN trial_days int NOT NULL DEFAULT 0 CHECK (trial_days BETWEEN 0 AND 365);

-- +goose Down
ALTER TABLE plan_versions DROP COLUMN trial_days;
