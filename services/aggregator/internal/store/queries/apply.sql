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
-- @mode picks the merge: "max" keeps the greater quantity, "last" keeps the quantity of the later event by (last_at, last_event_id bytewise), anything else adds.
INSERT INTO usage_aggregates (tenant_id, period_id, customer_id, meter_code, generation, quantity, quantity_scale, event_count, updated_at, last_at, last_event_id)
SELECT t.v, p.v, c.v, m.v, g.v, q.v::numeric, s.v, e.v, clock_timestamp(), NULLIF(la.v, '')::timestamptz, NULLIF(li.v, '')
FROM unnest(@tenants::uuid[]) WITH ORDINALITY AS t(v, n)
JOIN unnest(@periods::text[]) WITH ORDINALITY AS p(v, n) USING (n)
JOIN unnest(@customers::uuid[]) WITH ORDINALITY AS c(v, n) USING (n)
JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS m(v, n) USING (n)
JOIN unnest(@generations::int[]) WITH ORDINALITY AS g(v, n) USING (n)
JOIN unnest(@quantities::text[]) WITH ORDINALITY AS q(v, n) USING (n)
JOIN unnest(@scales::smallint[]) WITH ORDINALITY AS s(v, n) USING (n)
JOIN unnest(@event_counts::bigint[]) WITH ORDINALITY AS e(v, n) USING (n)
JOIN unnest(@last_ats::text[]) WITH ORDINALITY AS la(v, n) USING (n)
JOIN unnest(@last_ids::text[]) WITH ORDINALITY AS li(v, n) USING (n)
ORDER BY t.n
ON CONFLICT (tenant_id, period_id, customer_id, meter_code, generation) DO UPDATE
SET quantity = CASE @mode::text
      WHEN 'max' THEN GREATEST(usage_aggregates.quantity, EXCLUDED.quantity)
      WHEN 'last' THEN CASE WHEN (EXCLUDED.last_at, EXCLUDED.last_event_id COLLATE "C") > (usage_aggregates.last_at, usage_aggregates.last_event_id COLLATE "C")
                            THEN EXCLUDED.quantity ELSE usage_aggregates.quantity END
      ELSE usage_aggregates.quantity + EXCLUDED.quantity END,
    last_at = CASE WHEN @mode::text = 'last' AND (EXCLUDED.last_at, EXCLUDED.last_event_id COLLATE "C") > (usage_aggregates.last_at, usage_aggregates.last_event_id COLLATE "C")
                   THEN EXCLUDED.last_at ELSE usage_aggregates.last_at END,
    last_event_id = CASE WHEN @mode::text = 'last' AND (EXCLUDED.last_at, EXCLUDED.last_event_id COLLATE "C") > (usage_aggregates.last_at, usage_aggregates.last_event_id COLLATE "C")
                         THEN EXCLUDED.last_event_id ELSE usage_aggregates.last_event_id END,
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

-- name: UpsertUsageWindowAggregates :exec
-- The hour totals of the window quotas (migration 00004); @mode is the merge of UpsertUsageAggregates.
INSERT INTO usage_window_aggregates (tenant_id, customer_id, meter_code, generation, hour_id, quantity, quantity_scale, event_count, updated_at, last_at, last_event_id)
SELECT t.v, c.v, m.v, g.v, h.v, q.v::numeric, s.v, e.v, clock_timestamp(), NULLIF(la.v, '')::timestamptz, NULLIF(li.v, '')
FROM unnest(@tenants::uuid[]) WITH ORDINALITY AS t(v, n)
JOIN unnest(@customers::uuid[]) WITH ORDINALITY AS c(v, n) USING (n)
JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS m(v, n) USING (n)
JOIN unnest(@generations::int[]) WITH ORDINALITY AS g(v, n) USING (n)
JOIN unnest(@hour_ids::text[]) WITH ORDINALITY AS h(v, n) USING (n)
JOIN unnest(@quantities::text[]) WITH ORDINALITY AS q(v, n) USING (n)
JOIN unnest(@scales::smallint[]) WITH ORDINALITY AS s(v, n) USING (n)
JOIN unnest(@event_counts::bigint[]) WITH ORDINALITY AS e(v, n) USING (n)
JOIN unnest(@last_ats::text[]) WITH ORDINALITY AS la(v, n) USING (n)
JOIN unnest(@last_ids::text[]) WITH ORDINALITY AS li(v, n) USING (n)
ORDER BY t.n
ON CONFLICT (tenant_id, customer_id, meter_code, generation, hour_id) DO UPDATE
SET quantity = CASE @mode::text
      WHEN 'max' THEN GREATEST(usage_window_aggregates.quantity, EXCLUDED.quantity)
      WHEN 'last' THEN CASE WHEN (EXCLUDED.last_at, EXCLUDED.last_event_id COLLATE "C") > (usage_window_aggregates.last_at, usage_window_aggregates.last_event_id COLLATE "C")
                            THEN EXCLUDED.quantity ELSE usage_window_aggregates.quantity END
      ELSE usage_window_aggregates.quantity + EXCLUDED.quantity END,
    last_at = CASE WHEN @mode::text = 'last' AND (EXCLUDED.last_at, EXCLUDED.last_event_id COLLATE "C") > (usage_window_aggregates.last_at, usage_window_aggregates.last_event_id COLLATE "C")
                   THEN EXCLUDED.last_at ELSE usage_window_aggregates.last_at END,
    last_event_id = CASE WHEN @mode::text = 'last' AND (EXCLUDED.last_at, EXCLUDED.last_event_id COLLATE "C") > (usage_window_aggregates.last_at, usage_window_aggregates.last_event_id COLLATE "C")
                         THEN EXCLUDED.last_event_id ELSE usage_window_aggregates.last_event_id END,
    event_count = usage_window_aggregates.event_count + EXCLUDED.event_count, updated_at = EXCLUDED.updated_at;

-- name: PruneUsageWindowAggregates :execrows
-- Deletes the hour totals before @oldest_hour_id (every generation, every tenant): the rows are a rolling window.
DELETE FROM usage_window_aggregates WHERE hour_id < @oldest_hour_id::text;

-- name: UsageRangeMonths :many
-- One row per requested key, in request order: the combination of the month rows from_id..to_id of the customer's meter, by the key's mode (the sum, the maximum for
-- "max", the quantity of the latest event for "last"); found is false when there is no row. A single month is from_id = to_id.
SELECT COALESCE(a.quantity, '0')::text AS quantity, COALESCE(a.quantity_scale, 0)::int AS quantity_scale, COALESCE(a.event_count, 0)::bigint AS event_count,
       (a.event_count IS NOT NULL)::boolean AS found, COALESCE(to_char(a.last_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'), '')::text AS last_at, COALESCE(a.last_event_id, '')::text AS last_event_id
FROM unnest(@customers::uuid[]) WITH ORDINALITY AS c(v, n)
JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS m(v, n) USING (n)
JOIN unnest(@from_ids::text[]) WITH ORDINALITY AS f(v, n) USING (n)
JOIN unnest(@to_ids::text[]) WITH ORDINALITY AS t(v, n) USING (n)
JOIN unnest(@modes::text[]) WITH ORDINALITY AS x(v, n) USING (n)
LEFT JOIN LATERAL (
  SELECT (CASE x.v WHEN 'max' THEN max(u.quantity)
                   WHEN 'last' THEN (array_agg(u.quantity ORDER BY u.last_at DESC NULLS LAST, u.last_event_id COLLATE "C" DESC NULLS LAST))[1]
                   ELSE sum(u.quantity) END)::numeric AS quantity,
         max(u.quantity_scale)::int AS quantity_scale, sum(u.event_count)::bigint AS event_count,
         (array_agg(u.last_at ORDER BY u.last_at DESC NULLS LAST, u.last_event_id COLLATE "C" DESC NULLS LAST))[1] AS last_at,
         (array_agg(u.last_event_id ORDER BY u.last_at DESC NULLS LAST, u.last_event_id COLLATE "C" DESC NULLS LAST))[1] AS last_event_id
  FROM usage_aggregates u
  WHERE u.customer_id = c.v AND u.meter_code = m.v AND u.generation = @generation::int AND u.period_id BETWEEN f.v AND t.v
  HAVING count(*) > 0
) a ON true
ORDER BY c.n;

-- name: UsageRangeHours :many
-- UsageRangeMonths over the hour totals: the hour rows from_id..to_id.
SELECT COALESCE(a.quantity, '0')::text AS quantity, COALESCE(a.quantity_scale, 0)::int AS quantity_scale, COALESCE(a.event_count, 0)::bigint AS event_count,
       (a.event_count IS NOT NULL)::boolean AS found, COALESCE(to_char(a.last_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'), '')::text AS last_at, COALESCE(a.last_event_id, '')::text AS last_event_id
FROM unnest(@customers::uuid[]) WITH ORDINALITY AS c(v, n)
JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS m(v, n) USING (n)
JOIN unnest(@from_ids::text[]) WITH ORDINALITY AS f(v, n) USING (n)
JOIN unnest(@to_ids::text[]) WITH ORDINALITY AS t(v, n) USING (n)
JOIN unnest(@modes::text[]) WITH ORDINALITY AS x(v, n) USING (n)
LEFT JOIN LATERAL (
  SELECT (CASE x.v WHEN 'max' THEN max(u.quantity)
                   WHEN 'last' THEN (array_agg(u.quantity ORDER BY u.last_at DESC NULLS LAST, u.last_event_id COLLATE "C" DESC NULLS LAST))[1]
                   ELSE sum(u.quantity) END)::numeric AS quantity,
         max(u.quantity_scale)::int AS quantity_scale, sum(u.event_count)::bigint AS event_count,
         (array_agg(u.last_at ORDER BY u.last_at DESC NULLS LAST, u.last_event_id COLLATE "C" DESC NULLS LAST))[1] AS last_at,
         (array_agg(u.last_event_id ORDER BY u.last_at DESC NULLS LAST, u.last_event_id COLLATE "C" DESC NULLS LAST))[1] AS last_event_id
  FROM usage_window_aggregates u
  WHERE u.customer_id = c.v AND u.meter_code = m.v AND u.generation = @generation::int AND u.hour_id BETWEEN f.v AND t.v
  HAVING count(*) > 0
) a ON true
ORDER BY c.n;

-- name: UsageDistinctMonths :many
-- UNIQUE: the number of distinct values among the on-time events of the months from_id..to_id (a value in two months counts once); the first column is that count
-- and found says whether there is any value.
SELECT COALESCE(a.n, 0)::bigint AS distinct_values, (COALESCE(a.n, 0) > 0)::boolean AS found
FROM unnest(@customers::uuid[]) WITH ORDINALITY AS c(v, n)
JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS m(v, n) USING (n)
JOIN unnest(@from_ids::text[]) WITH ORDINALITY AS f(v, n) USING (n)
JOIN unnest(@to_ids::text[]) WITH ORDINALITY AS t(v, n) USING (n)
LEFT JOIN LATERAL (
  SELECT count(DISTINCT u.value_hash) AS n
  FROM usage_distinct_values u
  WHERE u.customer_id = c.v AND u.meter_code = m.v AND u.generation = @generation::int AND u.period_id BETWEEN f.v AND t.v AND u.billed_period_id = ''
) a ON true
ORDER BY c.n;

-- name: UsageDistinctHours :many
-- UsageDistinctMonths over the hour sets: the distinct values among the on-time events of the hours from_id..to_id.
SELECT COALESCE(a.n, 0)::bigint AS distinct_values, (COALESCE(a.n, 0) > 0)::boolean AS found
FROM unnest(@customers::uuid[]) WITH ORDINALITY AS c(v, n)
JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS m(v, n) USING (n)
JOIN unnest(@from_ids::text[]) WITH ORDINALITY AS f(v, n) USING (n)
JOIN unnest(@to_ids::text[]) WITH ORDINALITY AS t(v, n) USING (n)
LEFT JOIN LATERAL (
  SELECT count(DISTINCT u.value_hash) AS n
  FROM usage_distinct_hours u
  WHERE u.customer_id = c.v AND u.meter_code = m.v AND u.generation = @generation::int AND u.hour_id BETWEEN f.v AND t.v
) a ON true
ORDER BY c.n;

-- name: InsertDistinctValues :many
-- Claims the (period, value) pairs of a batch's UNIQUE events, in key order. Returns the pairs that were new, with the state they were written in: those are the
-- values the batch adds to the counts. A pair that already exists is handled by MergeDistinctValues.
INSERT INTO usage_distinct_values (tenant_id, customer_id, meter_code, generation, period_id, value_hash, billed_period_id)
SELECT t.v, c.v, m.v, g.v, p.v, h.v, b.v
FROM unnest(@tenants::uuid[]) WITH ORDINALITY AS t(v, n)
JOIN unnest(@customers::uuid[]) WITH ORDINALITY AS c(v, n) USING (n)
JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS m(v, n) USING (n)
JOIN unnest(@generations::int[]) WITH ORDINALITY AS g(v, n) USING (n)
JOIN unnest(@periods::text[]) WITH ORDINALITY AS p(v, n) USING (n)
JOIN unnest(@hashes::bytea[]) WITH ORDINALITY AS h(v, n) USING (n)
JOIN unnest(@billed::text[]) WITH ORDINALITY AS b(v, n) USING (n)
ORDER BY t.n
ON CONFLICT DO NOTHING
RETURNING tenant_id, customer_id, meter_code, generation, period_id, value_hash, billed_period_id;

-- name: MergeDistinctValues :many
-- Moves the state of values that exist to the minimum of the stored and the given state (domain.MergeSeen), locking their rows in key order, and returns the
-- ones that moved with both states, so the caller takes the unit of count from the old state's row and puts it on the new one's. A row that is already at or below
-- the given state is not touched.
WITH input AS (
  SELECT t.v AS tenant_id, c.v AS customer_id, m.v AS meter_code, g.v AS generation, p.v AS period_id, h.v AS value_hash, b.v AS billed
  FROM unnest(@tenants::uuid[]) WITH ORDINALITY AS t(v, n)
  JOIN unnest(@customers::uuid[]) WITH ORDINALITY AS c(v, n) USING (n)
  JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS m(v, n) USING (n)
  JOIN unnest(@generations::int[]) WITH ORDINALITY AS g(v, n) USING (n)
  JOIN unnest(@periods::text[]) WITH ORDINALITY AS p(v, n) USING (n)
  JOIN unnest(@hashes::bytea[]) WITH ORDINALITY AS h(v, n) USING (n)
  JOIN unnest(@billed::text[]) WITH ORDINALITY AS b(v, n) USING (n)
), cand AS (
  SELECT d.tenant_id, d.customer_id, d.meter_code, d.generation, d.period_id, d.value_hash, d.billed_period_id AS old_billed, i.billed AS new_billed
  FROM usage_distinct_values d
  JOIN input i ON (d.tenant_id, d.customer_id, d.meter_code, d.generation, d.period_id, d.value_hash) = (i.tenant_id, i.customer_id, i.meter_code, i.generation, i.period_id, i.value_hash)
  WHERE i.billed COLLATE "C" < d.billed_period_id COLLATE "C"
  ORDER BY d.tenant_id, d.customer_id, d.meter_code, d.generation, d.period_id, d.value_hash
  FOR UPDATE OF d
)
UPDATE usage_distinct_values u SET billed_period_id = c.new_billed
FROM cand c
WHERE (u.tenant_id, u.customer_id, u.meter_code, u.generation, u.period_id, u.value_hash) = (c.tenant_id, c.customer_id, c.meter_code, c.generation, c.period_id, c.value_hash)
RETURNING u.tenant_id, u.customer_id, u.meter_code, u.generation, u.period_id, c.old_billed::text AS old_billed, c.new_billed::text AS new_billed;

-- name: InsertDistinctHours :exec
-- The distinct values of the on-time UNIQUE events per hour.
INSERT INTO usage_distinct_hours (tenant_id, customer_id, meter_code, generation, hour_id, value_hash)
SELECT t.v, c.v, m.v, g.v, h.v, x.v
FROM unnest(@tenants::uuid[]) WITH ORDINALITY AS t(v, n)
JOIN unnest(@customers::uuid[]) WITH ORDINALITY AS c(v, n) USING (n)
JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS m(v, n) USING (n)
JOIN unnest(@generations::int[]) WITH ORDINALITY AS g(v, n) USING (n)
JOIN unnest(@hour_ids::text[]) WITH ORDINALITY AS h(v, n) USING (n)
JOIN unnest(@hashes::bytea[]) WITH ORDINALITY AS x(v, n) USING (n)
ORDER BY t.n
ON CONFLICT DO NOTHING;

-- name: PruneUsageDistinctHours :execrows
-- Deletes the hour sets before @oldest_hour_id, like PruneUsageWindowAggregates.
DELETE FROM usage_distinct_hours WHERE hour_id < @oldest_hour_id::text;
