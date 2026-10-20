-- name: SetTransactionTimeout :exec
SELECT set_config('transaction_timeout', @timeout::text, true);

-- name: SetAppTenant :exec
SELECT set_config('app.tenant_id', @tenant_id::text, true);

-- name: InsertProcessedEvents :many
-- Returns the (event, generation) pairs that were new; the others were applied before.
INSERT INTO processed_events (tenant_id, event_id, generation, period_id)
SELECT t.v, e.v, g.v, p.v
FROM unnest(@tenants::uuid[]) WITH ORDINALITY AS t(v, n)
JOIN unnest(@event_ids::text[]) WITH ORDINALITY AS e(v, n) USING (n)
JOIN unnest(@generations::int[]) WITH ORDINALITY AS g(v, n) USING (n)
JOIN unnest(@periods::text[]) WITH ORDINALITY AS p(v, n) USING (n)
ORDER BY t.n
ON CONFLICT DO NOTHING
RETURNING tenant_id, event_id, generation;

-- name: AdvanceOffset :execrows
-- Fenced: the offset never moves backwards, and a write that did not move it affects no row.
INSERT INTO consumer_offsets (consumer_group, topic, partition, next_offset)
VALUES (@consumer_group, @topic, @partition, @next_offset)
ON CONFLICT (consumer_group, topic, partition) DO UPDATE
SET next_offset = EXCLUDED.next_offset, updated_at = now()
WHERE consumer_offsets.next_offset <= EXCLUDED.next_offset;

-- name: UpsertUsageAggregates :exec
-- @is_max picks the merge: GREATEST for a max meter, a sum otherwise.
INSERT INTO usage_aggregates (tenant_id, period_id, customer_id, meter_code, generation, quantity, quantity_scale, event_count, updated_at)
SELECT t.v, p.v, c.v, m.v, g.v, q.v::numeric, s.v, e.v, clock_timestamp()
FROM unnest(@tenants::uuid[]) WITH ORDINALITY AS t(v, n)
JOIN unnest(@periods::text[]) WITH ORDINALITY AS p(v, n) USING (n)
JOIN unnest(@customers::uuid[]) WITH ORDINALITY AS c(v, n) USING (n)
JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS m(v, n) USING (n)
JOIN unnest(@generations::int[]) WITH ORDINALITY AS g(v, n) USING (n)
JOIN unnest(@quantities::text[]) WITH ORDINALITY AS q(v, n) USING (n)
JOIN unnest(@scales::smallint[]) WITH ORDINALITY AS s(v, n) USING (n)
JOIN unnest(@event_counts::bigint[]) WITH ORDINALITY AS e(v, n) USING (n)
ORDER BY t.n
ON CONFLICT (tenant_id, period_id, customer_id, meter_code, generation) DO UPDATE
SET quantity = CASE WHEN @is_max::boolean THEN GREATEST(usage_aggregates.quantity, EXCLUDED.quantity) ELSE usage_aggregates.quantity + EXCLUDED.quantity END,
    event_count = usage_aggregates.event_count + EXCLUDED.event_count, updated_at = EXCLUDED.updated_at;

-- name: UpsertLateUsage :exec
-- @is_max picks the merge: GREATEST for a max meter, a sum otherwise.
INSERT INTO late_usage (tenant_id, billed_period_id, customer_id, meter_code, origin_period_id, generation, quantity, quantity_scale, event_count, updated_at)
SELECT t.v, b.v, c.v, m.v, o.v, g.v, q.v::numeric, s.v, e.v, clock_timestamp()
FROM unnest(@tenants::uuid[]) WITH ORDINALITY AS t(v, n)
JOIN unnest(@billed_periods::text[]) WITH ORDINALITY AS b(v, n) USING (n)
JOIN unnest(@customers::uuid[]) WITH ORDINALITY AS c(v, n) USING (n)
JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS m(v, n) USING (n)
JOIN unnest(@origin_periods::text[]) WITH ORDINALITY AS o(v, n) USING (n)
JOIN unnest(@generations::int[]) WITH ORDINALITY AS g(v, n) USING (n)
JOIN unnest(@quantities::text[]) WITH ORDINALITY AS q(v, n) USING (n)
JOIN unnest(@scales::smallint[]) WITH ORDINALITY AS s(v, n) USING (n)
JOIN unnest(@event_counts::bigint[]) WITH ORDINALITY AS e(v, n) USING (n)
ORDER BY t.n
ON CONFLICT (tenant_id, billed_period_id, customer_id, meter_code, origin_period_id, generation) DO UPDATE
SET quantity = CASE WHEN @is_max::boolean THEN GREATEST(late_usage.quantity, EXCLUDED.quantity) ELSE late_usage.quantity + EXCLUDED.quantity END,
    event_count = late_usage.event_count + EXCLUDED.event_count, updated_at = EXCLUDED.updated_at;

-- name: UsageBatch :many
-- One row per requested key, in request order; found is false for a key without an aggregate row.
SELECT COALESCE(a.quantity, '0')::text AS quantity, COALESCE(a.quantity_scale, 0)::int AS quantity_scale, COALESCE(a.event_count, 0)::bigint AS event_count,
       (a.event_count IS NOT NULL)::boolean AS found
FROM unnest(@customers::uuid[]) WITH ORDINALITY AS c(v, n)
JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS m(v, n) USING (n)
JOIN unnest(@periods::text[]) WITH ORDINALITY AS p(v, n) USING (n)
LEFT JOIN (SELECT customer_id, meter_code, period_id, generation, quantity::text AS quantity, quantity_scale::int AS quantity_scale, event_count
           FROM usage_aggregates) a ON a.customer_id = c.v AND a.meter_code = m.v AND a.period_id = p.v AND a.generation = @generation::int
ORDER BY c.n;
