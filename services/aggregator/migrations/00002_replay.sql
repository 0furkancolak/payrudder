-- Replay (Phase 11): job state beyond the Phase 03 sketch.

-- +goose Up
ALTER TABLE replay_jobs
  ADD COLUMN source_generation int,
  ADD COLUMN verification      jsonb,
  ADD COLUMN last_error        text,
  ADD COLUMN updated_at        timestamptz NOT NULL DEFAULT now(),
  ADD COLUMN completed_at      timestamptz;
ALTER TABLE replay_jobs DROP CONSTRAINT replay_jobs_status_check;
ALTER TABLE replay_jobs ADD CONSTRAINT replay_jobs_status_check
  CHECK (status IN ('running', 'catching_up', 'ready', 'promoted', 'discarded', 'failed'));

-- Discarding a generation deletes what it built.
GRANT DELETE ON usage_aggregates, late_usage, processed_events TO aggregator_system;

-- +goose Down
ALTER TABLE replay_jobs DROP CONSTRAINT replay_jobs_status_check;
ALTER TABLE replay_jobs ADD CONSTRAINT replay_jobs_status_check
  CHECK (status IN ('pending', 'running', 'verifying', 'ready', 'promoted', 'failed'));
ALTER TABLE replay_jobs
  DROP COLUMN completed_at, DROP COLUMN updated_at, DROP COLUMN last_error, DROP COLUMN verification, DROP COLUMN source_generation;
REVOKE DELETE ON usage_aggregates, late_usage, processed_events FROM aggregator_system;
