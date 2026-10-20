-- name: ReceiveProviderEvent :execrows
INSERT INTO provider_events (provider, event_id, body, status) VALUES (@provider, @event_id, @body, 'received') ON CONFLICT DO NOTHING;

-- name: FinishProviderEvent :exec
UPDATE provider_events SET status = @status, tenant_id = @tenant_id, error = @error, processed_at = now()
WHERE provider = @provider AND event_id = @event_id;

-- name: ForgetProviderEvent :exec
DELETE FROM provider_events WHERE provider = @provider AND event_id = @event_id AND status IN ('received', 'failed');
