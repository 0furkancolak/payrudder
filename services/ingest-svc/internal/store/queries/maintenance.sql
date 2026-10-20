-- name: SetTransactionTimeout :exec
SELECT set_config('transaction_timeout', @timeout::text, true);

-- name: MaintainIdempotencyPartitions :one
-- Creates the partitions of the current and the next two months and drops those past the 45-day retention.
SELECT created::text[] AS created, dropped::text[] AS dropped FROM maintain_idempotency_partitions(@now::timestamptz, 2, interval '45 days');
