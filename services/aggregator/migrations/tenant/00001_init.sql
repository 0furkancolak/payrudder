-- aggregator_db, tenant schema (phase 28, ADR 0018): what one tenant of a cell owns, in its own schema t_<id>.
--
-- It is the sum of ../00001_init.sql to ../00005_*.sql written for a schema instead of public: the same tables, policies and privileges, but no name of a schema
-- anywhere, and functions that bind the schema they are created in (SET search_path FROM CURRENT), so these files build any tenant's schema. They run with the search
-- path set to that schema; each schema keeps its own goose_db_version. A change to ../ is a change here too: TestTenantSchemaMatchesPublic compares the two.
-- Roles are the service's own (aggregator_app, aggregator_system); the cell database has the same ones.
--
-- consumer_offsets is not here: the offsets of a consumer group belong to the group, not to a tenant, and stay in the pooled database.

-- +goose Up
-- Tenant of the current transaction, set by pgxx.InTenantTx. The tenant schema keeps row-level security as the second guard behind the search path.
CREATE FUNCTION app_tenant_id() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE
AS $$ SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid $$;

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

-- quantity is in scaled units (ADR 0010); quantity_scale (the meter's, immutable) turns it back into a decimal.
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

-- The inbox: one row per applied (event, generation). Not tenant-RLS'd: only the consumer touches it.
CREATE TABLE processed_events (
  tenant_id   uuid NOT NULL,
  event_id    text NOT NULL,
  generation  int  NOT NULL,
  period_id   text NOT NULL,
  PRIMARY KEY (period_id, tenant_id, event_id, generation)
) PARTITION BY LIST (period_id);

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

-- Hour totals for the quota windows, written under the same inbox as the month rows, for on-time events only: a rolling window that maintenance trims.
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

-- The distinct values of UNIQUE meters, per billing period and (on-time events only) per hour.
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

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['aggregate_generations', 'usage_aggregates', 'late_usage', 'period_settings', 'usage_window_aggregates', 'usage_distinct_values', 'usage_distinct_hours'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- ensure_processed_partition creates the inbox partition of a period ("YYYY-MM") if it is missing, in the schema it lives in.
-- +goose StatementBegin
CREATE FUNCTION ensure_processed_partition(p_period text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path FROM CURRENT
AS $$
DECLARE
  s     text := current_schema();
  pname text;
BEGIN
  IF p_period !~ '^[0-9]{4}-(0[1-9]|1[0-2])$' THEN
    RAISE EXCEPTION 'malformed period %', p_period USING ERRCODE = '22023';
  END IF;
  pname := 'processed_events_' || replace(p_period, '-', '_');
  IF to_regclass(format('%I.%I', s, pname)) IS NULL THEN
    EXECUTE format('CREATE TABLE IF NOT EXISTS %I.%I PARTITION OF %I.processed_events FOR VALUES IN (%L)', s, pname, s, p_period);
  END IF;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION ensure_processed_partition(text) FROM PUBLIC;

-- drop_old_processed_partitions drops inbox partitions whose period ended more than p_retention before p_now (period ends are taken in UTC plus one day of slack for
-- tenant time zones), in the schema it lives in. Returns the dropped names.
-- +goose StatementBegin
CREATE FUNCTION drop_old_processed_partitions(p_now timestamptz, p_retention interval DEFAULT interval '45 days')
RETURNS text[]
LANGUAGE plpgsql SECURITY DEFINER SET search_path FROM CURRENT
AS $$
DECLARE
  s       text := current_schema();
  r       record;
  m       date;
  dropped text[] := '{}';
BEGIN
  FOR r IN
    SELECT c.relname
    FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
    WHERE i.inhparent = to_regclass(format('%I.processed_events', s))
  LOOP
    CONTINUE WHEN r.relname !~ '^processed_events_[0-9]{4}_[0-9]{2}$';
    m := make_date(substr(r.relname, 18, 4)::int, substr(r.relname, 23, 2)::int, 1);
    IF ((m + interval '1 month' + interval '1 day')::timestamp AT TIME ZONE 'UTC') <= p_now - p_retention THEN
      EXECUTE format('DROP TABLE %I.%I', s, r.relname);
      dropped := dropped || r.relname::text;
    END IF;
  END LOOP;
  RETURN dropped;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION drop_old_processed_partitions(timestamptz, interval) FROM PUBLIC;

-- Grants. The app role reads aggregates and operates on period settings (freeze, early close) through three columns.
GRANT SELECT ON usage_aggregates, late_usage, aggregate_generations, period_settings, usage_window_aggregates, usage_distinct_values, usage_distinct_hours TO aggregator_app;
GRANT UPDATE (cutoff_at, early_closed_at, frozen_at) ON period_settings TO aggregator_app;
GRANT INSERT ON period_settings TO aggregator_app;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO aggregator_app;

GRANT SELECT, INSERT, UPDATE ON usage_aggregates, late_usage, period_settings TO aggregator_system;
GRANT SELECT, INSERT ON processed_events TO aggregator_system;
-- FOR SHARE needs an UPDATE privilege; the replay phase writes this table anyway.
GRANT SELECT, INSERT, UPDATE ON aggregate_generations TO aggregator_system;
GRANT SELECT, INSERT, UPDATE ON replay_jobs TO aggregator_system;
-- Discarding a generation or resetting a sandbox deletes what it built; the consumer writes the window totals and the value sets, maintenance trims them.
GRANT DELETE ON usage_aggregates, late_usage, processed_events, period_settings TO aggregator_system;
GRANT SELECT, INSERT, UPDATE, DELETE ON usage_window_aggregates, usage_distinct_values, usage_distinct_hours TO aggregator_system;
GRANT EXECUTE ON FUNCTION app_tenant_id(), ensure_processed_partition(text), drop_old_processed_partitions(timestamptz, interval)
  TO aggregator_system;

-- +goose Down
DROP FUNCTION drop_old_processed_partitions(timestamptz, interval);
DROP FUNCTION ensure_processed_partition(text);
DROP TABLE usage_distinct_hours, usage_distinct_values, usage_window_aggregates, replay_jobs, period_settings, processed_events, late_usage, usage_aggregates, aggregate_generations;
DROP FUNCTION app_tenant_id();
