-- name: ReceiveProviderEvent :execrows
-- A notification seen before is a duplicate, unless the delivery that recorded it never finished: a row still received or failed for longer than the window (the process died
-- between this write and its answer, or the "failed" mark and the clean-up both failed) is taken over by the provider's retry, which processes it again. The effects are
-- idempotent: the ledger keeps a fact once, billing settles a payment once, a subscription follows the newest event.
INSERT INTO provider_events (provider, event_id, body, status) VALUES (@provider, @event_id, @body, 'received')
ON CONFLICT (provider, event_id) DO UPDATE
SET body = EXCLUDED.body, status = 'received', error = NULL, processed_at = NULL, received_at = now()
WHERE provider_events.status IN ('received', 'failed') AND provider_events.received_at < now() - make_interval(secs => @reclaim_after_secs::double precision);

-- name: FinishProviderEvent :exec
UPDATE provider_events SET status = @status, tenant_id = @tenant_id, error = @error, processed_at = now()
WHERE provider = @provider AND event_id = @event_id;

-- name: ForgetProviderEvent :exec
DELETE FROM provider_events WHERE provider = @provider AND event_id = @event_id AND status IN ('received', 'failed');
