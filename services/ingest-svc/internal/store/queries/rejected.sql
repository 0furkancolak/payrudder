-- The rejected-event viewer (docs/plans/18-quick-integration.md, work item 3).

-- name: InsertRejectedEvents :exec
INSERT INTO rejected_events (tenant_id, error_code, error_message, meter_code, customer_external_id, event_time, idempotency_key)
SELECT @tenant_id::uuid, c.v, m.v, mc.v, ce.v, NULLIF(et.v, '0001-01-01 00:00:00+00'::timestamptz), ik.v
FROM unnest(@error_codes::text[]) WITH ORDINALITY AS c(v, n)
JOIN unnest(@error_messages::text[]) WITH ORDINALITY AS m(v, n) USING (n)
JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS mc(v, n) USING (n)
JOIN unnest(@customer_ids::text[]) WITH ORDINALITY AS ce(v, n) USING (n)
JOIN unnest(@event_times::timestamptz[]) WITH ORDINALITY AS et(v, n) USING (n)
JOIN unnest(@idempotency_keys::text[]) WITH ORDINALITY AS ik(v, n) USING (n);

-- name: TrimRejectedEvents :execrows
-- Keeps the newest @max_rows rows of the tenant that are not older than @max_age.
DELETE FROM rejected_events
WHERE tenant_id = @tenant_id::uuid
  AND (received_at < now() - make_interval(days => @max_days::int)
    OR id IN (SELECT r.id FROM rejected_events r WHERE r.tenant_id = @tenant_id::uuid ORDER BY r.received_at DESC, r.id DESC OFFSET @max_rows::int));

-- name: SweepRejectedEvents :execrows
-- Drops what is past its age in every tenant (ingest_system); the writer trims only the tenants that still get rejections.
DELETE FROM rejected_events WHERE received_at < now() - make_interval(days => @max_days::int);

-- name: ListRejectedEvents :many
SELECT id, error_code, error_message, meter_code, customer_external_id, event_time, idempotency_key, received_at
FROM rejected_events
WHERE tenant_id = @tenant_id::uuid
  AND received_at >= now() - make_interval(days => @max_days::int)
  AND (@error_code::text = '' OR error_code = @error_code::text)
  AND (@meter_code::text = '' OR meter_code = @meter_code::text)
  AND (received_at, id) < (@after_time::timestamptz, @after_id::uuid)
ORDER BY received_at DESC, id DESC
LIMIT @page_size::int;

-- name: RejectedEventCodeCounts :many
SELECT error_code, count(*)::int AS count
FROM rejected_events
WHERE tenant_id = @tenant_id::uuid AND received_at >= now() - interval '24 hours'
GROUP BY error_code
ORDER BY count(*) DESC, error_code;

-- name: RejectedEventHourCounts :many
-- 24 hourly buckets ending with the current hour, empty hours included.
SELECT h.hour::timestamptz AS hour, count(r.id)::int AS rejected
FROM generate_series(date_trunc('hour', now()) - interval '23 hours', date_trunc('hour', now()), interval '1 hour') AS h(hour)
LEFT JOIN rejected_events r ON r.tenant_id = @tenant_id::uuid AND r.received_at >= h.hour AND r.received_at < h.hour + interval '1 hour'
GROUP BY h.hour
ORDER BY h.hour;

-- Resetting a sandbox tenant (ingest_system, internal/sandboxreset).
-- name: ResetRejectedEvents :execrows
DELETE FROM rejected_events WHERE tenant_id = $1;
