-- Security review (plan 22, "Not covered" of G3 and G4): TRUNCATE of the audit log and of the evidence of what a customer accepted. The row triggers of audit_events,
-- contract_acceptances and contract_snapshots do not fire on TRUNCATE. Nobody but the table owner holds the privilege (the app and system roles are granted none), but a
-- migration, a script or a person at a psql prompt is exactly who this is for: these three tables are never truncated. One exception, the one the product has: a tenant move
-- into a cell (pkg/pgxx Move) empties the destination schema before it copies the rows, and that schema is the tenant's own, named by the session setting
-- payrudder.tenant_move (t_ and the tenant id without dashes); a table of the pooled database (public) is never truncated. A statement trigger also fires for the tables a
-- TRUNCATE ... CASCADE reaches.
--
-- What the protection is, exactly (the earlier migrations said "for everybody, the owner included", which was more than they do): the grants (the app role may INSERT and
-- SELECT, the system role INSERT, SELECT and, for the audit log, DELETE for a tenant move; neither holds UPDATE or TRUNCATE on them) and these triggers, which refuse UPDATE,
-- DELETE and TRUNCATE for the table's owner too. It is not tamper-proof: the owner role and a superuser can disable or drop a trigger and change anything, and a role that
-- holds DELETE can delete one tenant's audit rows by naming that tenant in payrudder.tenant_move (the move's own path), or a sandbox tenant's or a cell tenant's acceptances
-- (the reset's and the move's path). There is no hash chain and no copy outside the database.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION evidence_no_truncate() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_TABLE_SCHEMA = 't_' || replace(current_setting('payrudder.tenant_move', true), '-', '') THEN
    RETURN NULL;
  END IF;
  RAISE EXCEPTION 'rows of % are evidence and are never truncated', TG_TABLE_NAME USING ERRCODE = 'P0001', HINT = 'evidence_table_truncate';
END $$;
-- +goose StatementEnd
CREATE TRIGGER audit_events_no_truncate BEFORE TRUNCATE ON audit_events FOR EACH STATEMENT EXECUTE FUNCTION evidence_no_truncate();
CREATE TRIGGER contract_acceptances_no_truncate BEFORE TRUNCATE ON contract_acceptances FOR EACH STATEMENT EXECUTE FUNCTION evidence_no_truncate();
CREATE TRIGGER contract_snapshots_no_truncate BEFORE TRUNCATE ON contract_snapshots FOR EACH STATEMENT EXECUTE FUNCTION evidence_no_truncate();

-- +goose Down
DROP TRIGGER contract_snapshots_no_truncate ON contract_snapshots;
DROP TRIGGER contract_acceptances_no_truncate ON contract_acceptances;
DROP TRIGGER audit_events_no_truncate ON audit_events;
DROP FUNCTION evidence_no_truncate();
