-- Plan 22 (G4), the hash chain: every entry of the audit log carries the hash of the one before it, so an entry that was changed, deleted from the middle, put in or put
-- in another order is found by `payrudderctl audit verify` (pkg/audit.Verify). The chain belongs to a tenant (rows with no tenant are the chain "none"): a tenant that moves
-- between the pooled database and a cell takes its chain with it, and the rows of other tenants are not part of it.
--
-- seq numbers an entry in its chain (1, 2, 3, ...), prev_hash is the row_hash of the entry before it ('' for the first), row_hash is the SHA-256 of the entry's own
-- columns and prev_hash (audit_events_hash: the database is the one place that writes the canonical form down, so the writer and the verifier cannot disagree). A BEFORE
-- INSERT trigger fills the three under an advisory lock of the chain, so two writers of one chain never read the same predecessor: that is the whole price (the entries
-- of one tenant, or of the chain "none", are written one at a time until their transactions commit). The copy of a tenant between databases (payrudder.tenant_move)
-- keeps the chain the rows have.
--
-- Entries that exist when this runs are numbered by (created_at, id) of their chain, in this migration, with the append-only trigger off
-- for its length: that is the one time rows of the log are updated.
--
-- Retention: payrudderctl audit prune deletes a prefix of a chain (payrudder.audit_prune) and appends the entry audit.pruned, which names the last row it deleted, so the
-- verifier accepts a chain that starts after seq 1 only when such an entry says so.

-- +goose Up
ALTER TABLE audit_events
  ADD COLUMN seq bigint,
  ADD COLUMN prev_hash text NOT NULL DEFAULT '' CHECK (length(prev_hash) IN (0, 64)),
  ADD COLUMN row_hash text NOT NULL DEFAULT '' CHECK (length(row_hash) IN (0, 64));

-- +goose StatementBegin
CREATE FUNCTION audit_events_hash(e audit_events) RETURNS text
LANGUAGE sql AS $$
  SELECT encode(sha256(convert_to(jsonb_build_array(e.prev_hash, e.seq, e.id, e.tenant_id, e.account_id, e.actor_type, e.actor_id, e.actor_email, e.action, e.target_type,
    e.target_id, e.summary, e.detail, e.before_hash, e.after_hash, e.source_ip, e.request_id, to_char(e.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US'))::text, 'UTF8')), 'hex')
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION audit_events_chain_lock(chain uuid) RETURNS void
LANGUAGE sql AS $$
  SELECT pg_advisory_xact_lock(hashtextextended('audit_events:' || 'audit_events'::regclass::oid::text || ':' || coalesce(chain::text, ''), 0))
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE
  r record;
  chain uuid;
  n bigint := 0;
  prev text := '';
BEGIN
  ALTER TABLE audit_events DISABLE TRIGGER audit_events_append_only;
  FOR r IN SELECT id, coalesce(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid) AS k FROM audit_events ORDER BY k, created_at, id LOOP
    IF chain IS DISTINCT FROM r.k THEN
      chain := r.k;
      n := 0;
      prev := '';
    END IF;
    n := n + 1;
    UPDATE audit_events SET seq = n, prev_hash = prev WHERE id = r.id;
    UPDATE audit_events a SET row_hash = audit_events_hash(a) WHERE a.id = r.id RETURNING a.row_hash INTO prev;
  END LOOP;
  ALTER TABLE audit_events ENABLE TRIGGER audit_events_append_only;
END $$;
-- +goose StatementEnd

ALTER TABLE audit_events ALTER COLUMN seq SET NOT NULL;
CREATE UNIQUE INDEX audit_events_chain ON audit_events ((coalesce(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid)), seq);

-- +goose StatementBegin
CREATE FUNCTION audit_events_chain() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  last_seq bigint;
  last_hash text;
BEGIN
  -- The copy of a tenant between databases keeps the chain its rows have (the move compares the rows of both sides).
  IF NEW.tenant_id IS NOT NULL AND current_setting('payrudder.tenant_move', true) = NEW.tenant_id::text THEN
    RETURN NEW;
  END IF;
  PERFORM audit_events_chain_lock(NEW.tenant_id);
  SELECT seq, row_hash INTO last_seq, last_hash FROM audit_events
    WHERE coalesce(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid) = coalesce(NEW.tenant_id, '00000000-0000-0000-0000-000000000000'::uuid) ORDER BY seq DESC LIMIT 1;
  NEW.seq := coalesce(last_seq, 0) + 1;
  NEW.prev_hash := coalesce(last_hash, '');
  NEW.row_hash := audit_events_hash(NEW);
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER audit_events_chain BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION audit_events_chain();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND current_setting('payrudder.tenant_move', true) = OLD.tenant_id::text THEN
    RETURN OLD;
  END IF;
  -- Retention (payrudderctl audit prune): a prefix of a chain, with the entry audit.pruned that names where it ended appended in the same transaction.
  IF TG_OP = 'DELETE' AND current_setting('payrudder.audit_prune', true) = 'on' THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'the audit log is append-only' USING ERRCODE = 'P0001', HINT = 'audit_events_append_only';
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER audit_events_chain ON audit_events;
DROP FUNCTION audit_events_chain();
DROP INDEX audit_events_chain;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND current_setting('payrudder.tenant_move', true) = OLD.tenant_id::text THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'the audit log is append-only' USING ERRCODE = 'P0001', HINT = 'audit_events_append_only';
END $$;
-- +goose StatementEnd
DROP FUNCTION audit_events_chain_lock(uuid);
DROP FUNCTION audit_events_hash(audit_events);
ALTER TABLE audit_events DROP COLUMN row_hash, DROP COLUMN prev_hash, DROP COLUMN seq;
