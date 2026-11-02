-- Security review (plan 22, "Not covered" of G4): TRUNCATE of the audit log. The row trigger of audit_events does not fire on TRUNCATE, and nobody but the table owner
-- holds the privilege (no role of the service is granted it), but a migration, a script or a person at a psql prompt is exactly who this is for. One exception, the one
-- the product has: a tenant move into a cell (pkg/pgxx Move) empties the destination schema before it copies the rows, and that schema is the tenant's own, named by the
-- session setting payrudder.tenant_move (t_ and the tenant id without dashes); a table of the pooled database (public) is never truncated.
--
-- What the protection is, exactly (00006 said "for everybody, the owner included", which was more than it does): the grants (the system role holds INSERT, SELECT and DELETE
-- for a tenant move, the app role nothing since 00008; neither holds UPDATE or TRUNCATE) and the triggers, which refuse UPDATE and DELETE and now TRUNCATE for the table's owner too. It is not tamper-proof: the owner
-- role and a superuser can disable or drop a trigger and change anything, and a role that holds DELETE can delete one tenant's rows by naming that tenant in
-- payrudder.tenant_move (the move's own path). There is no hash chain and no copy outside the database. The ledger and the receipts are covered by 00009.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION audit_events_no_truncate() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_TABLE_SCHEMA = 't_' || replace(current_setting('payrudder.tenant_move', true), '-', '') THEN
    RETURN NULL;
  END IF;
  RAISE EXCEPTION 'rows of % are evidence and are never truncated', TG_TABLE_NAME USING ERRCODE = 'P0001', HINT = 'evidence_table_truncate';
END $$;
-- +goose StatementEnd
CREATE TRIGGER audit_events_no_truncate BEFORE TRUNCATE ON audit_events FOR EACH STATEMENT EXECUTE FUNCTION audit_events_no_truncate();

-- +goose Down
DROP TRIGGER audit_events_no_truncate ON audit_events;
DROP FUNCTION audit_events_no_truncate();
