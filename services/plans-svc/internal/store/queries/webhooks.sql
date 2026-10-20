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

-- name: HasWebhookTargets :one
SELECT EXISTS (SELECT 1 FROM webhook_endpoints WHERE tenant_id = app_tenant_id() AND active AND (cardinality(event_types) = 0 OR @event_type::text = ANY(event_types)));
