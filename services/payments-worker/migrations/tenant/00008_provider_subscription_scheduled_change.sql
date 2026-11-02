-- payments_db, tenant schema: ../00007 as it ends up, for a schema. Plan 06 (Paddle parity, finding F9): a change the provider has announced for a subscription it runs and will apply later. A cancellation "at the end of the period" leaves the
-- provider's status at "active" until the day, so the status alone hides it. Paddle's subscription has scheduled_change {action cancel|pause|resume, effective_at, resume_at}
-- (developer.paddle.com/api-reference/subscriptions/get-subscription.md, read 2026-11-02); every notification of a subscription carries the whole entity, so the columns are
-- replaced with each event and cleared by one that has none. Read-only for people: nothing here changes the provider's subscription.
-- +goose Up
ALTER TABLE provider_subscriptions
  ADD COLUMN scheduled_action text,   -- the provider's word (cancel, pause or resume today); not constrained, so a new one never makes a notification fail
  ADD COLUMN scheduled_effective_at timestamptz,
  ADD COLUMN scheduled_resume_at timestamptz,
  ADD CONSTRAINT provider_subscriptions_scheduled_whole CHECK ((scheduled_action IS NULL) = (scheduled_effective_at IS NULL));

-- +goose Down
ALTER TABLE provider_subscriptions
  DROP CONSTRAINT provider_subscriptions_scheduled_whole,
  DROP COLUMN scheduled_resume_at, DROP COLUMN scheduled_effective_at, DROP COLUMN scheduled_action;
