-- name: InsertWebhookEndpoint :one
INSERT INTO webhook_endpoints (tenant_id, id, url, description, event_types, secret_enc)
VALUES (app_tenant_id(), @id, @url, @description, @event_types, @secret_enc)
RETURNING id, url, description, event_types, active, created_at;

-- name: ListWebhookEndpoints :many
SELECT id, url, description, event_types, active, created_at FROM webhook_endpoints WHERE tenant_id = app_tenant_id() ORDER BY created_at, id;

-- name: CountWebhookEndpoints :one
SELECT count(*) FROM webhook_endpoints WHERE tenant_id = app_tenant_id();

-- name: GetWebhookEndpoint :one
SELECT id, url, description, event_types, active, created_at FROM webhook_endpoints WHERE tenant_id = app_tenant_id() AND id = @id;

-- name: UpdateWebhookEndpoint :one
UPDATE webhook_endpoints SET url = @url, description = @description, event_types = @event_types, active = @active
WHERE tenant_id = app_tenant_id() AND id = @id
RETURNING id, url, description, event_types, active, created_at;

-- name: DeleteWebhookEndpoint :execrows
DELETE FROM webhook_endpoints WHERE tenant_id = app_tenant_id() AND id = @id;

-- name: RotateWebhookSecret :one
UPDATE webhook_endpoints SET prev_secret_enc = secret_enc, prev_secret_until = @until, secret_enc = @secret_enc
WHERE tenant_id = app_tenant_id() AND id = @id
RETURNING id, url, description, event_types, active, created_at;

-- name: InsertWebhookEvent :execrows
INSERT INTO webhook_events (tenant_id, id, type, data, occurred_at) VALUES (app_tenant_id(), @id, @type, @data, @occurred_at)
ON CONFLICT (tenant_id, id) DO NOTHING;

-- name: QueueWebhookDeliveries :execrows
INSERT INTO webhook_deliveries (tenant_id, id, event_id, endpoint_id)
SELECT app_tenant_id(), gen_random_uuid(), @event_id::uuid, e.id FROM webhook_endpoints e
WHERE e.tenant_id = app_tenant_id() AND e.active AND (cardinality(e.event_types) = 0 OR @event_type::text = ANY(e.event_types))
ON CONFLICT (tenant_id, event_id, endpoint_id) DO NOTHING;

-- name: QueueWebhookDeliveryFor :one
INSERT INTO webhook_deliveries (tenant_id, id, event_id, endpoint_id) VALUES (app_tenant_id(), @id, @event_id, @endpoint_id)
ON CONFLICT (tenant_id, event_id, endpoint_id) DO UPDATE SET state = 'pending', attempts = 0, next_attempt_at = now(), last_error = NULL, last_status = NULL, delivered_at = NULL
RETURNING id;

-- name: ListWebhookDeliveries :many
SELECT d.id, d.event_id, ev.type AS event_type, d.endpoint_id, d.state, d.attempts, d.next_attempt_at, d.last_status, d.last_error, d.created_at, d.delivered_at
FROM webhook_deliveries d JOIN webhook_events ev ON ev.tenant_id = d.tenant_id AND ev.id = d.event_id
WHERE d.tenant_id = app_tenant_id() AND (@endpoint_id::uuid = '00000000-0000-0000-0000-000000000000' OR d.endpoint_id = @endpoint_id)
  AND (@state::text = '' OR d.state = @state)
  AND (d.created_at, d.id) < (@after_time::timestamptz, @after_id::uuid)
ORDER BY d.created_at DESC, d.id DESC LIMIT @page_size;

-- name: GetWebhookDelivery :one
SELECT d.id, d.event_id, ev.type AS event_type, d.endpoint_id, d.state, d.attempts, d.next_attempt_at, d.last_status, d.last_error, d.created_at, d.delivered_at
FROM webhook_deliveries d JOIN webhook_events ev ON ev.tenant_id = d.tenant_id AND ev.id = d.event_id
WHERE d.tenant_id = app_tenant_id() AND d.id = @id;

-- name: RedeliverWebhook :one
UPDATE webhook_deliveries SET state = 'pending', attempts = 0, next_attempt_at = now(), last_error = NULL, last_status = NULL, delivered_at = NULL
WHERE tenant_id = app_tenant_id() AND id = @id
RETURNING id;

-- name: ClaimDueWebhookDeliveries :many
WITH args AS (
  SELECT @now::timestamptz AS at, @lease_secs::float8 AS lease
), due AS (
  SELECT tenant_id, id FROM webhook_deliveries WHERE state = 'pending' AND next_attempt_at <= (SELECT at FROM args)
  ORDER BY next_attempt_at LIMIT @batch FOR UPDATE SKIP LOCKED)
UPDATE webhook_deliveries d SET attempts = d.attempts + 1, next_attempt_at = args.at + make_interval(secs => args.lease), last_attempt_at = args.at
FROM due, args WHERE d.tenant_id = due.tenant_id AND d.id = due.id
RETURNING d.tenant_id, d.id, d.event_id, d.endpoint_id, d.attempts;

-- name: GetWebhookDeliveryTarget :one
SELECT e.url, e.active, e.secret_enc, e.prev_secret_enc, e.prev_secret_until, ev.type, ev.data, ev.occurred_at
FROM webhook_deliveries d
JOIN webhook_endpoints e ON e.tenant_id = d.tenant_id AND e.id = d.endpoint_id
JOIN webhook_events ev ON ev.tenant_id = d.tenant_id AND ev.id = d.event_id
WHERE d.tenant_id = @tenant_id AND d.id = @id;

-- name: RescheduleWebhookDelivery :exec
UPDATE webhook_deliveries SET next_attempt_at = @next_attempt_at, last_status = NULLIF(@last_status::int, 0), last_error = @last_error::text
WHERE tenant_id = @tenant_id AND id = @id;

-- name: FinishWebhookDelivery :exec
UPDATE webhook_deliveries SET state = @state::text, last_status = NULLIF(@last_status::int, 0), last_error = NULLIF(@last_error::text, ''),
  delivered_at = CASE WHEN @state::text = 'delivered' THEN @delivered_at::timestamptz ELSE NULL END
WHERE tenant_id = @tenant_id AND id = @id;

-- name: PruneWebhookEvents :exec
DELETE FROM webhook_events WHERE created_at < @before;

-- name: HasWebhookTargets :one
SELECT EXISTS (SELECT 1 FROM webhook_endpoints WHERE tenant_id = app_tenant_id() AND active AND (cardinality(event_types) = 0 OR @event_type::text = ANY(event_types)));
