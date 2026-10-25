-- Resetting a sandbox tenant (phase 45 E). Run as ingest_system from internal/sandboxreset only; every statement names the tenant.

-- The dedup keys: after a reset the same idempotency key is a new event again.
-- name: ResetIdempotencyKeys :execrows
DELETE FROM idempotency_keys WHERE tenant_id = $1;

-- Events accepted but not yet on the topic.
-- name: ResetOutbox :execrows
DELETE FROM outbox WHERE tenant_id = $1;
