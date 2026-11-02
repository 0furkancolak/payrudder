-- Retention, schedules and appeals of live tenants (plan 14). All on the system role.

-- name: GetTenantLifecycle :one
SELECT * FROM tenant_lifecycle WHERE live_tenant_id = @live_tenant_id;

-- A ban starts the retention period; leaving the ban ends it (the commands decide which, in the transaction of the status change).
-- name: SetTenantRetention :exec
INSERT INTO tenant_lifecycle (live_tenant_id, retention_until, retention_due_at, updated_at) VALUES (@live_tenant_id, @retention_until, NULL, now())
ON CONFLICT (live_tenant_id) DO UPDATE SET retention_until = EXCLUDED.retention_until, retention_due_at = NULL, updated_at = now();

-- name: ClearTenantRetention :exec
UPDATE tenant_lifecycle SET retention_until = NULL, retention_due_at = NULL, updated_at = now() WHERE live_tenant_id = @live_tenant_id AND retention_until IS NOT NULL;

-- name: SetTenantSchedule :one
INSERT INTO tenant_lifecycle (live_tenant_id, scheduled_status, scheduled_at, scheduled_category, scheduled_reason, scheduled_by, updated_at)
VALUES (@live_tenant_id, @scheduled_status, @scheduled_at, @scheduled_category, @scheduled_reason, @scheduled_by, now())
ON CONFLICT (live_tenant_id) DO UPDATE SET scheduled_status = EXCLUDED.scheduled_status, scheduled_at = EXCLUDED.scheduled_at, scheduled_category = EXCLUDED.scheduled_category,
  scheduled_reason = EXCLUDED.scheduled_reason, scheduled_by = EXCLUDED.scheduled_by, updated_at = now()
RETURNING *;

-- name: ClearTenantSchedule :execrows
UPDATE tenant_lifecycle SET scheduled_status = NULL, scheduled_at = NULL, scheduled_category = '', scheduled_reason = '', scheduled_by = '', updated_at = now()
WHERE live_tenant_id = @live_tenant_id AND scheduled_at IS NOT NULL;

-- The schedules that are due, oldest first.
-- name: ListDueTenantSchedules :many
SELECT * FROM tenant_lifecycle WHERE scheduled_at IS NOT NULL AND scheduled_at <= @now::timestamptz ORDER BY scheduled_at LIMIT @batch;

-- Marks the banned tenants whose retention period has ended; a tenant that is not banned any more has no deadline (the status change cleared it).
-- name: MarkRetentionDue :many
UPDATE tenant_lifecycle l SET retention_due_at = @now::timestamptz, updated_at = now()
FROM tenants t
WHERE t.id = l.live_tenant_id AND t.status = 'banned' AND l.retention_until IS NOT NULL AND l.retention_until <= @now::timestamptz AND l.retention_due_at IS NULL
RETURNING l.live_tenant_id, l.retention_until;

-- name: InsertTenantAppeal :one
INSERT INTO tenant_appeals (id, live_tenant_id, status, held_status, statement, submitted_by) VALUES (@id, @live_tenant_id, 'open', @held_status, @statement, @submitted_by)
RETURNING *;

-- name: GetTenantAppeal :one
SELECT * FROM tenant_appeals WHERE id = @id;

-- name: DecideTenantAppeal :one
UPDATE tenant_appeals SET status = @status, answer = @answer, decided_by = @decided_by, decided_at = now() WHERE id = @id AND status = 'open' RETURNING *;

-- name: LatestTenantAppeal :one
SELECT * FROM tenant_appeals WHERE live_tenant_id = @live_tenant_id ORDER BY created_at DESC, id DESC LIMIT 1;

-- The latest 200, newest first; an empty tenant or status matches every one.
-- name: ListTenantAppeals :many
SELECT a.*, ap.name AS app_name FROM tenant_appeals a
JOIN tenants t ON t.id = a.live_tenant_id
JOIN apps ap ON ap.id = t.app_id
WHERE (@live_tenant_id::uuid = '00000000-0000-0000-0000-000000000000'::uuid OR a.live_tenant_id = @live_tenant_id::uuid) AND (@status::text = '' OR a.status = @status::text)
ORDER BY a.created_at DESC, a.id DESC LIMIT 200;
