-- The transactional outbox and the tenant-event outbox.

-- name: InsertOutbox :exec
INSERT INTO outbox (tenant_id, topic, partition_key, payload, headers, message_id)
VALUES (@tenant_id, @topic, @partition_key, @payload, @headers, @message_id)
ON CONFLICT (message_id) DO NOTHING;

-- name: NotifyOutbox :exec
SELECT pg_notify(@channel::text, '');

-- name: InsertTenantEvent :exec
INSERT INTO tenant_event_outbox (tenant_id, event_id, type, data, occurred_at) VALUES (@tenant_id, @event_id, @type, @data, @occurred_at)
ON CONFLICT DO NOTHING;

-- name: ListTenantEvents :many
SELECT tenant_id, event_id, type, data, occurred_at FROM tenant_event_outbox ORDER BY created_at LIMIT 200;

-- name: DeleteTenantEvent :exec
DELETE FROM tenant_event_outbox WHERE tenant_id = @tenant_id AND event_id = @event_id;
