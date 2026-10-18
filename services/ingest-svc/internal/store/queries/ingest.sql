-- name: ClockNow :one
SELECT clock_timestamp()::timestamptz AS now;

-- name: InsertIdempotencyKeys :many
-- Returns the keys that were new. Rows that already exist in this month's bucket are skipped silently.
INSERT INTO idempotency_keys (tenant_id, idempotency_key, event_id, payload_hash, created_at)
SELECT @tenant_id::uuid, k.key, e.id, h.hash, @created_at::timestamptz
FROM unnest(@keys::text[]) WITH ORDINALITY AS k(key, n)
JOIN unnest(@event_ids::text[]) WITH ORDINALITY AS e(id, n) USING (n)
JOIN unnest(@hashes::bytea[]) WITH ORDINALITY AS h(hash, n) USING (n)
ON CONFLICT DO NOTHING
RETURNING idempotency_key;

-- name: FindIdempotencyKeys :many
SELECT idempotency_key, event_id, payload_hash, created_at
FROM idempotency_keys
WHERE tenant_id = @tenant_id::uuid
  AND idempotency_key = ANY(@keys::text[])
  AND created_at = ANY(@buckets::timestamptz[]);

-- name: EnqueueOutbox :exec
SELECT enqueue_outbox(@tenant_id::uuid, @topic::text, @partition_key::text, @payloads::bytea[], @headers::text[], @ingested_at::timestamptz);
