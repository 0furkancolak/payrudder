-- aggregator_db schema (docs/05-data-model.md#aggregator_db).
-- Roles: aggregator_migrator owns everything; aggregator_app (read APIs, RLS enforced); aggregator_system (the consumer
-- and maintenance, BYPASSRLS: one batch spans tenants).

-- +goose Up
-- Tenant of the current transaction, set by pgxx.InTenantTx.
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
-- A tenant without a row reads and writes generation 1.

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

-- Schema only until the replay phase (Phase 11).
CREATE TABLE replay_jobs (
  id                 uuid PRIMARY KEY,
  tenant_id          uuid NOT NULL,
  from_period_id     text NOT NULL,
  to_period_id       text NOT NULL,
  target_generation  int  NOT NULL,
  status             text NOT NULL CHECK (status IN ('pending', 'running', 'verifying', 'ready', 'promoted', 'failed')),
  start_offsets      jsonb,
  end_offsets        jsonb,
  dual_write_from    jsonb,
  reason             text NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (tenant_id, target_generation) REFERENCES aggregate_generations (tenant_id, generation)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['aggregate_generations', 'usage_aggregates', 'late_usage', 'period_settings'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

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
GRANT EXECUTE ON FUNCTION app_tenant_id() TO aggregator_app;

GRANT SELECT, INSERT, UPDATE ON usage_aggregates, late_usage, period_settings, consumer_offsets TO aggregator_system;
GRANT SELECT, INSERT ON processed_events TO aggregator_system;
-- FOR SHARE needs an UPDATE privilege; the replay phase writes this table anyway.
GRANT SELECT, INSERT, UPDATE ON aggregate_generations TO aggregator_system;
GRANT SELECT, INSERT, UPDATE ON replay_jobs TO aggregator_system;
GRANT EXECUTE ON FUNCTION app_tenant_id(), ensure_processed_partition(text), drop_old_processed_partitions(timestamptz, interval)
  TO aggregator_system;

-- +goose Down
DROP FUNCTION drop_old_processed_partitions(timestamptz, interval);
DROP FUNCTION ensure_processed_partition(text);
DROP TABLE replay_jobs, period_settings, consumer_offsets, processed_events, late_usage, usage_aggregates, aggregate_generations;
DROP FUNCTION app_tenant_id();
