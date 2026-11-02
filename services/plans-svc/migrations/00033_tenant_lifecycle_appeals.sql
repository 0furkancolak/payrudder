-- Plan 14: what the operator does to a live tenant over time, and what its owners may say back. Global tables like tenant_status_events (no tenant_id: they are about a live
-- tenant, the pooled database holds them for ever, and only the operator's and identity modules on the system role touch them).
--  * tenant_lifecycle: one row per tenant that has one. The retention deadline of a banned tenant (its data is kept until then; a job marks it due and deletes nothing) and
--    the suspension or freeze the operator scheduled for it (a sweeper applies it).
--  * tenant_appeals: an owner's statement against a hold, and the operator's decision. One open appeal per tenant, enforced by the database.

-- +goose Up
CREATE TABLE tenant_lifecycle (
  live_tenant_id     uuid PRIMARY KEY REFERENCES tenants (id),
  retention_until    timestamptz,
  retention_due_at   timestamptz,
  scheduled_status   text CHECK (scheduled_status IN ('suspended', 'frozen')),
  scheduled_at       timestamptz,
  scheduled_category text NOT NULL DEFAULT '' CHECK (scheduled_category IN ('', 'nonpayment', 'fraud', 'abuse', 'legal', 'request', 'other')),
  scheduled_reason   text NOT NULL DEFAULT '' CHECK (length(scheduled_reason) <= 500),
  scheduled_by       text NOT NULL DEFAULT '' CHECK (length(scheduled_by) <= 120),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CHECK ((scheduled_status IS NULL) = (scheduled_at IS NULL)),
  CHECK (retention_due_at IS NULL OR retention_until IS NOT NULL)
);
-- The sweeper looks for schedules that are due and for deadlines that passed; both are rare.
CREATE INDEX tenant_lifecycle_scheduled ON tenant_lifecycle (scheduled_at) WHERE scheduled_at IS NOT NULL;
CREATE INDEX tenant_lifecycle_retention ON tenant_lifecycle (retention_until) WHERE retention_until IS NOT NULL AND retention_due_at IS NULL;
GRANT SELECT, INSERT, UPDATE ON tenant_lifecycle TO plans_system;

CREATE TABLE tenant_appeals (
  id             uuid PRIMARY KEY,
  live_tenant_id uuid NOT NULL REFERENCES tenants (id),
  status         text NOT NULL CHECK (status IN ('open', 'accepted', 'rejected')),
  held_status    text NOT NULL CHECK (held_status IN ('suspended', 'frozen', 'banned')),
  statement      text NOT NULL CHECK (length(statement) BETWEEN 1 AND 2000),
  submitted_by   text NOT NULL CHECK (length(submitted_by) BETWEEN 1 AND 320),
  answer         text NOT NULL DEFAULT '' CHECK (length(answer) <= 1000),
  decided_by     text NOT NULL DEFAULT '' CHECK (length(decided_by) <= 120),
  created_at     timestamptz NOT NULL DEFAULT now(),
  decided_at     timestamptz,
  CHECK ((status = 'open') = (decided_at IS NULL))
);
CREATE UNIQUE INDEX tenant_appeals_one_open ON tenant_appeals (live_tenant_id) WHERE status = 'open';
CREATE INDEX tenant_appeals_by_tenant ON tenant_appeals (live_tenant_id, created_at DESC, id DESC);
CREATE INDEX tenant_appeals_by_status ON tenant_appeals (status, created_at DESC, id DESC);
GRANT SELECT, INSERT, UPDATE ON tenant_appeals TO plans_system;

-- +goose Down
DROP TABLE tenant_appeals;
DROP TABLE tenant_lifecycle;
