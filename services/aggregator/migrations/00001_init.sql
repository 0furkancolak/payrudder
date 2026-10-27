-- aggregator_db schema (docs/05-data-model.md#aggregator_db).
-- This is the baseline: it was written once, before the first release, from the schema the pre-release migrations had built, and it starts the history.
-- Every change from here on is a new numbered migration (00002_...), never an edit of this file.
-- Roles: aggregator_migrator owns everything; aggregator_app (read APIs, RLS enforced); aggregator_system (the consumer
-- and maintenance, BYPASSRLS: one batch spans tenants).

-- +goose Up

-- Tenant of the current transaction, set by pgxx.InTenantTx.
-- +goose StatementBegin
CREATE FUNCTION app_tenant_id() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE
AS $$ SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid $$;
-- +goose StatementEnd

CREATE TABLE aggregate_generations (
  tenant_id    uuid        NOT NULL,
  generation   int         NOT NULL CHECK (generation >= 1),
  status       text        NOT NULL CHECK (status IN ('building', 'catching_up', 'active', 'retired', 'discarded')),
  created_at   timestamptz NOT NULL DEFAULT now(),
  promoted_at  timestamptz,
  PRIMARY KEY (tenant_id, generation)
);
CREATE UNIQUE INDEX one_active_generation ON aggregate_generations (tenant_id) WHERE status = 'active';
CREATE UNIQUE INDEX one_open_replay       ON aggregate_generations (tenant_id) WHERE status IN ('building', 'catching_up');
-- A tenant without a row reads and writes generation 1.

-- quantity is in scaled units (ADR 0010); quantity_scale (the meter's, immutable) turns it back into a decimal.
-- last_at and last_event_id are the LAST aggregation's: the event the quantity is the value of, its time (microseconds, what timestamptz holds) and its id. A row is
-- merged by keeping the later (last_at, last_event_id COLLATE "C"), so the result does not depend on the order events arrive in. They are NULL for every other
-- aggregation (docs/03-services/aggregator.md, "UNIQUE and LAST").
CREATE TABLE usage_aggregates (
  tenant_id       uuid        NOT NULL,
  period_id       text        NOT NULL,
  customer_id     uuid        NOT NULL,
  meter_code      text        NOT NULL,
  generation      int         NOT NULL,
  quantity        numeric(38, 0) NOT NULL,
  quantity_scale  smallint    NOT NULL CHECK (quantity_scale BETWEEN 0 AND 6),
  event_count     bigint      NOT NULL,
  updated_at      timestamptz NOT NULL,
  last_at         timestamptz,
  last_event_id   text,
  PRIMARY KEY (tenant_id, period_id, customer_id, meter_code, generation)
);

CREATE TABLE late_usage (
  tenant_id         uuid        NOT NULL,
  billed_period_id  text        NOT NULL,
  customer_id       uuid        NOT NULL,
  meter_code        text        NOT NULL,
  origin_period_id  text        NOT NULL,
  generation        int         NOT NULL,
  quantity          numeric(38, 0) NOT NULL,
  quantity_scale    smallint    NOT NULL CHECK (quantity_scale BETWEEN 0 AND 6),
  event_count       bigint      NOT NULL,
  updated_at        timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, billed_period_id, customer_id, meter_code, origin_period_id, generation)
);

-- Hour totals, so that limits-svc can reconcile its hour, day and ISO-week quota counters the way it reconciles the monthly ones (docs/03-services/aggregator.md,
-- "Window totals"). One row per customer, meter and wall-clock hour of the tenant ("2026-11-05T13"), written in the same transaction and under the same inbox as the month
-- row of usage_aggregates, for on-time events only. A day or a week is the sum (or, for a MAX meter, the maximum) of its hour rows; a year or the lifetime is read from the
-- month rows, which are kept forever.
--
-- The rows are a rolling window, not a ledger: billing never reads them, maintenance deletes the hours older than two weeks, and an event whose hour is older than that is not
-- written at all. period_id is the month the hour belongs to, so a replay carries the hours of the periods it did not rebuild over to the new generation like the month rows.
CREATE TABLE usage_window_aggregates (
  tenant_id       uuid        NOT NULL,
  customer_id     uuid        NOT NULL,
  meter_code      text        NOT NULL,
  generation      int         NOT NULL,
  hour_id         text        NOT NULL,
  period_id       text        GENERATED ALWAYS AS (left(hour_id, 7)) STORED,
  quantity        numeric(38, 0) NOT NULL,
  quantity_scale  smallint    NOT NULL CHECK (quantity_scale BETWEEN 0 AND 6),
  event_count     bigint      NOT NULL,
  updated_at      timestamptz NOT NULL,
  last_at         timestamptz,
  last_event_id   text,
  PRIMARY KEY (tenant_id, customer_id, meter_code, generation, hour_id)
);
CREATE INDEX usage_window_aggregates_hour ON usage_window_aggregates (hour_id);

-- UNIQUE meters keep the set of distinct values, because a distinct count is not additive: a year or a day cannot be read from the counts of its months or hours.
-- One row per value (its SHA-256, as a property can be 4 KiB) per customer, meter and billing period, and per wall-clock hour for the quota windows. The
-- count of the billing period stays in usage_aggregates.quantity (and in late_usage for values first seen after the cutoff), kept equal to the number of rows
-- in each state by the transaction that changes them.
--
-- billed_period_id is '' when an on-time event carried the value, else the earliest period a late event carrying it is billed in. '' sorts before every
-- period id, so the state of a value is the minimum of what its events say, which is commutative, associative and idempotent (domain.MergeSeen).
CREATE TABLE usage_distinct_values (
  tenant_id         uuid        NOT NULL,
  customer_id       uuid        NOT NULL,
  meter_code        text        NOT NULL,
  generation        int         NOT NULL,
  period_id         text        NOT NULL,
  value_hash        bytea       NOT NULL CHECK (length(value_hash) = 32),
  billed_period_id  text        NOT NULL DEFAULT '',
  PRIMARY KEY (tenant_id, customer_id, meter_code, generation, period_id, value_hash)
);

-- Only on-time events, like usage_window_aggregates: a rolling window, deleted with the hour totals.
CREATE TABLE usage_distinct_hours (
  tenant_id    uuid   NOT NULL,
  customer_id  uuid   NOT NULL,
  meter_code   text   NOT NULL,
  generation   int    NOT NULL,
  hour_id      text   NOT NULL,
  value_hash   bytea  NOT NULL CHECK (length(value_hash) = 32),
  period_id    text   GENERATED ALWAYS AS (left(hour_id, 7)) STORED,
  PRIMARY KEY (tenant_id, customer_id, meter_code, generation, hour_id, value_hash)
);
CREATE INDEX usage_distinct_hours_hour ON usage_distinct_hours (hour_id);

-- The inbox: one row per applied (event, generation). Not tenant-RLS'd: only the consumer touches it.
CREATE TABLE processed_events (
  tenant_id   uuid NOT NULL,
  event_id    text NOT NULL,
  generation  int  NOT NULL,
  period_id   text NOT NULL,
  PRIMARY KEY (period_id, tenant_id, event_id, generation)
) PARTITION BY LIST (period_id);

CREATE TABLE consumer_offsets (
  consumer_group  text        NOT NULL,
  topic           text        NOT NULL,
  partition       int         NOT NULL,
  next_offset     bigint      NOT NULL CHECK (next_offset >= 0),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (consumer_group, topic, partition)
);

CREATE TABLE period_settings (
  tenant_id        uuid        NOT NULL,
  period_id        text        NOT NULL,
  timezone         text        NOT NULL,
  period_start     timestamptz NOT NULL,
  period_end       timestamptz NOT NULL,
  grace_period     interval    NOT NULL,
  cutoff_at        timestamptz NOT NULL,
  early_closed_at  timestamptz,
  frozen_at        timestamptz,
  PRIMARY KEY (tenant_id, period_id)
);

-- Replay (docs/03-services/aggregator.md): the state of a job that rebuilds a generation.
CREATE TABLE replay_jobs (
  id                 uuid PRIMARY KEY,
  tenant_id          uuid NOT NULL,
  from_period_id     text NOT NULL,
  to_period_id       text NOT NULL,
  target_generation  int  NOT NULL,
  status             text NOT NULL CHECK (status IN ('running', 'catching_up', 'ready', 'promoted', 'discarded', 'failed')),
  start_offsets      jsonb,
  end_offsets        jsonb,
  dual_write_from    jsonb,
  reason             text NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),
  source_generation  int,
  verification       jsonb,
  last_error         text,
  updated_at         timestamptz NOT NULL DEFAULT now(),
  completed_at       timestamptz,
  FOREIGN KEY (tenant_id, target_generation) REFERENCES aggregate_generations (tenant_id, generation)
);

-- Row-level security on the tables of a tenant; FORCE applies it to the owner too (aggregator_system has BYPASSRLS).
ALTER TABLE aggregate_generations ENABLE ROW LEVEL SECURITY;
ALTER TABLE aggregate_generations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON aggregate_generations USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE usage_aggregates ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_aggregates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON usage_aggregates USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE late_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE late_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON late_usage USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE period_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE period_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON period_settings USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE usage_window_aggregates ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_window_aggregates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON usage_window_aggregates USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE usage_distinct_values ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_distinct_values FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON usage_distinct_values USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE usage_distinct_hours ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_distinct_hours FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON usage_distinct_hours USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

-- ensure_processed_partition creates the inbox partition of a period ("YYYY-MM") if it is missing.
-- +goose StatementBegin
CREATE FUNCTION ensure_processed_partition(p_period text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public
AS $$
DECLARE pname text;
BEGIN
  IF p_period !~ '^[0-9]{4}-(0[1-9]|1[0-2])$' THEN
    RAISE EXCEPTION 'malformed period %', p_period USING ERRCODE = '22023';
  END IF;
  pname := 'processed_events_' || replace(p_period, '-', '_');
  IF to_regclass(format('public.%I', pname)) IS NULL THEN
    EXECUTE format('CREATE TABLE IF NOT EXISTS public.%I PARTITION OF public.processed_events FOR VALUES IN (%L)', pname, p_period);
  END IF;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION ensure_processed_partition(text) FROM PUBLIC;

-- drop_old_processed_partitions drops inbox partitions whose period ended more than p_retention before p_now
-- (period ends are taken in UTC plus one day of slack for tenant time zones). Returns the dropped names.
-- +goose StatementBegin
CREATE FUNCTION drop_old_processed_partitions(p_now timestamptz, p_retention interval DEFAULT interval '45 days')
RETURNS text[]
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public
AS $$
DECLARE r record; m date; dropped text[] := '{}';
BEGIN
  FOR r IN
    SELECT c.relname
    FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
    WHERE i.inhparent = 'public.processed_events'::regclass
  LOOP
    CONTINUE WHEN r.relname !~ '^processed_events_[0-9]{4}_[0-9]{2}$';
    m := make_date(substr(r.relname, 18, 4)::int, substr(r.relname, 23, 2)::int, 1);
    IF ((m + interval '1 month' + interval '1 day')::timestamp AT TIME ZONE 'UTC') <= p_now - p_retention THEN
      EXECUTE format('DROP TABLE public.%I', r.relname);
      dropped := dropped || r.relname::text;
    END IF;
  END LOOP;
  RETURN dropped;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION drop_old_processed_partitions(timestamptz, interval) FROM PUBLIC;

-- Grants. The app role reads aggregates and operates on period settings (freeze, early close) through three columns.
GRANT SELECT ON usage_aggregates, late_usage, aggregate_generations, period_settings, consumer_offsets TO aggregator_app;
GRANT UPDATE (cutoff_at, early_closed_at, frozen_at) ON period_settings TO aggregator_app;
GRANT INSERT ON period_settings TO aggregator_app;
GRANT SELECT ON usage_window_aggregates, usage_distinct_values, usage_distinct_hours TO aggregator_app;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO aggregator_app;

GRANT SELECT, INSERT, UPDATE ON usage_aggregates, late_usage, period_settings, consumer_offsets TO aggregator_system;
GRANT SELECT, INSERT ON processed_events TO aggregator_system;
-- FOR SHARE needs an UPDATE privilege; the replay phase writes this table anyway.
GRANT SELECT, INSERT, UPDATE ON aggregate_generations TO aggregator_system;
GRANT SELECT, INSERT, UPDATE ON replay_jobs TO aggregator_system;
-- Discarding a generation deletes what it built; resetting a sandbox deletes the tenant's period settings too.
GRANT DELETE ON usage_aggregates, late_usage, processed_events, period_settings TO aggregator_system;
-- The consumer writes the hour totals and the distinct values; maintenance and a discarded or reset generation delete them.
GRANT SELECT, INSERT, UPDATE, DELETE ON usage_window_aggregates, usage_distinct_values, usage_distinct_hours TO aggregator_system;
GRANT EXECUTE ON FUNCTION app_tenant_id(), ensure_processed_partition(text), drop_old_processed_partitions(timestamptz, interval)
  TO aggregator_system;

-- +goose Down
DROP FUNCTION drop_old_processed_partitions(timestamptz, interval);
DROP FUNCTION ensure_processed_partition(text);
DROP TABLE usage_distinct_hours, usage_distinct_values, usage_window_aggregates, replay_jobs, period_settings, consumer_offsets, processed_events, late_usage, usage_aggregates,
  aggregate_generations;
DROP FUNCTION app_tenant_id();
