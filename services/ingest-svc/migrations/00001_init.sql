-- ingest_db schema (docs/05-data-model.md#ingest_db).
-- This is the baseline: it was written once, before the first release, from the schema the pre-release migrations had built, and it starts the history.
-- Every change from here on is a new numbered migration (00002_...), never an edit of this file.
-- Roles: ingest_migrator owns everything; ingest_app (request handlers, RLS enforced); ingest_system (BYPASSRLS: relay,
-- partition maintenance, watermark).

-- +goose Up
-- Tenant of the current transaction, set by pgxx.InTenantTx. NULLIF: a pooled connection that saw a transaction-local
-- set_config returns '' afterwards, not NULL.
CREATE FUNCTION app_tenant_id() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE
AS $$ SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid $$;

-- created_at is the start (UTC) of the month the key was first seen, not now(): Postgres requires the partition key
-- in the primary key, so uniqueness is "per key per month". The write path also checks the two previous months' buckets,
-- which gives a dedup window of at least two and less than three months (> the 35-day max lateness).
CREATE TABLE idempotency_keys (
  tenant_id        uuid        NOT NULL,
  idempotency_key  text        NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
  event_id         text        NOT NULL,
  payload_hash     bytea       NOT NULL,
  created_at       timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, idempotency_key, created_at)
) PARTITION BY RANGE (created_at);

ALTER TABLE idempotency_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE idempotency_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON idempotency_keys
  USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

-- Not tenant-RLS'd: handlers never read it. They enqueue through enqueue_outbox(); the relay (ingest_system) reads it.
CREATE TABLE outbox (
  id             bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id      uuid        NOT NULL,
  topic          text        NOT NULL,
  partition_key  text        NOT NULL,
  payload        bytea       NOT NULL,
  headers        jsonb       NOT NULL,
  ingested_at    timestamptz NOT NULL
);
CREATE INDEX outbox_ingested_at ON outbox (ingested_at);

-- enqueue_outbox inserts one batch for the caller's own tenant and wakes the relay. NOTIFY is delivered at commit.
-- headers arrive as text[] (JSON documents) because that round-trips through every driver.
-- +goose StatementBegin
CREATE FUNCTION enqueue_outbox(
  p_tenant        uuid,
  p_topic         text,
  p_partition_key text,
  p_payloads      bytea[],
  p_headers       text[],
  p_ingested_at   timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public
AS $$
BEGIN
  IF p_tenant IS DISTINCT FROM app_tenant_id() THEN
    RAISE EXCEPTION 'enqueue_outbox: tenant mismatch' USING ERRCODE = '42501';
  END IF;
  IF cardinality(p_payloads) <> cardinality(p_headers) THEN
    RAISE EXCEPTION 'enqueue_outbox: payloads and headers differ in length' USING ERRCODE = '22023';
  END IF;
  INSERT INTO outbox (tenant_id, topic, partition_key, payload, headers, ingested_at)
  SELECT p_tenant, p_topic, p_partition_key, u.payload, u.headers::jsonb, p_ingested_at
  FROM unnest(p_payloads, p_headers) WITH ORDINALITY AS u(payload, headers, ord)
  ORDER BY u.ord;
  PERFORM pg_notify('outbox_new', '');
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION enqueue_outbox(uuid, text, text, bytea[], text[], timestamptz) FROM PUBLIC;

-- maintain_idempotency_partitions creates the partitions of the current month and the next p_ahead months, and drops
-- partitions that ended more than p_retention before p_now. It returns the names it created and dropped.
-- +goose StatementBegin
CREATE FUNCTION maintain_idempotency_partitions(
  p_now        timestamptz,
  p_ahead      int      DEFAULT 2,
  p_retention  interval DEFAULT interval '45 days',
  OUT created  text[],
  OUT dropped  text[]
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public
AS $$
DECLARE
  m     date;
  lo    timestamptz;
  hi    timestamptz;
  pname text;
  r     record;
BEGIN
  created := '{}';
  dropped := '{}';
  FOR i IN 0..p_ahead LOOP
    m := (date_trunc('month', p_now AT TIME ZONE 'UTC') + make_interval(months => i))::date;
    pname := format('idempotency_keys_y%sm%s', to_char(m, 'YYYY'), to_char(m, 'MM'));
    IF to_regclass(format('public.%I', pname)) IS NULL THEN
      lo := m::timestamp AT TIME ZONE 'UTC';
      hi := (m + interval '1 month')::timestamp AT TIME ZONE 'UTC';
      EXECUTE format('CREATE TABLE public.%I PARTITION OF public.idempotency_keys FOR VALUES FROM (%L) TO (%L)', pname, lo, hi);
      created := created || pname;
    END IF;
  END LOOP;

  FOR r IN
    SELECT c.relname
    FROM pg_inherits i
    JOIN pg_class c ON c.oid = i.inhrelid
    WHERE i.inhparent = 'public.idempotency_keys'::regclass
  LOOP
    CONTINUE WHEN r.relname !~ '^idempotency_keys_y[0-9]{4}m[0-9]{2}$';
    m := make_date(substr(r.relname, 19, 4)::int, substr(r.relname, 24, 2)::int, 1);
    IF ((m + interval '1 month')::timestamp AT TIME ZONE 'UTC') <= p_now - p_retention THEN
      EXECUTE format('DROP TABLE public.%I', r.relname);
      dropped := dropped || r.relname::text;
    END IF;
  END LOOP;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION maintain_idempotency_partitions(timestamptz, int, interval) FROM PUBLIC;

SELECT maintain_idempotency_partitions(now());

GRANT SELECT, INSERT ON idempotency_keys TO ingest_app;
GRANT EXECUTE ON FUNCTION app_tenant_id(), enqueue_outbox(uuid, text, text, bytea[], text[], timestamptz) TO ingest_app;

-- SELECT ... FOR UPDATE SKIP LOCKED needs an UPDATE privilege on at least one column. id is GENERATED ALWAYS, so this
-- grant lets the relay lock rows without letting it change anything.
GRANT SELECT, DELETE, UPDATE (id) ON outbox TO ingest_system;
GRANT EXECUTE ON FUNCTION maintain_idempotency_partitions(timestamptz, int, interval) TO ingest_system;
-- Resetting a sandbox deletes the tenant's dedup keys.
GRANT SELECT, DELETE ON idempotency_keys TO ingest_system;

-- +goose Down
DROP FUNCTION maintain_idempotency_partitions(timestamptz, int, interval);
DROP FUNCTION enqueue_outbox(uuid, text, text, bytea[], text[], timestamptz);
DROP TABLE outbox, idempotency_keys;
DROP FUNCTION app_tenant_id();
