-- assets_db, tenant schema (phase 28, ADR 0018): what one tenant of a cell owns, in its own schema t_<id>.
--
-- It is ../00001_init.sql run in a schema: nothing in it names a schema, and it has no function that binds one, so the same statements build the tables, policies
-- and grants in public (the pooled database) and in t_<id>. They run with the search path set to the tenant's schema; each schema keeps its own goose_db_version.
-- A change to ../ is a change here too: TestTenantSchemaMatchesPublic compares the two. Roles are the service's own (assets_app, assets_system).

-- +goose Up
CREATE FUNCTION app_tenant_id() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE
AS $$ SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid $$;

CREATE TABLE upload_sessions (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  idempotency_key  text,
  status           text NOT NULL CHECK (status IN ('OPEN','COMPLETED','EXPIRED','ABORTED')),
  expires_at       timestamptz NOT NULL,                -- created_at + 24 h
  created_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, idempotency_key)
);
CREATE INDEX upload_sessions_expiry ON upload_sessions (expires_at) WHERE status = 'OPEN';

CREATE TABLE assets (
  tenant_id        uuid NOT NULL,
  id               uuid NOT NULL,
  session_id       uuid,                                -- NULL for proxied form-data uploads
  purpose          text NOT NULL CHECK (purpose IN ('invoice_logo','invoice_attachment','general')),
  filename         text NOT NULL,                       -- sanitized, display only
  content_type     text NOT NULL,
  declared_size    bigint,
  size_bytes       bigint,                              -- from HeadObject after completion
  sha256           bytea,                               -- declared or computed
  object_key       text NOT NULL,                       -- {tenant_id}/assets/{id}
  s3_upload_id     text,                                -- multipart UploadId while uploading
  part_size        bigint,
  part_count       int,
  status           text NOT NULL CHECK (status IN ('PENDING','UPLOADING','SCANNING','READY','REJECTED','ABORTED','CORRUPT','DELETED')),
  reject_reason    text,
  verified_at      timestamptz,                         -- the background verifier's pass (content sniffing, sha256)
  created_at       timestamptz NOT NULL DEFAULT now(),
  completed_at     timestamptz,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, session_id) REFERENCES upload_sessions (tenant_id, id)
);
CREATE INDEX assets_by_session ON assets (tenant_id, session_id);
CREATE INDEX assets_unverified ON assets (created_at) WHERE status = 'READY' AND verified_at IS NULL AND session_id IS NOT NULL;

CREATE TABLE storage_usage (                            -- quota accounting, updated in the completion transaction
  tenant_id uuid PRIMARY KEY, bytes_used bigint NOT NULL DEFAULT 0 CHECK (bytes_used >= 0)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['upload_sessions', 'assets', 'storage_usage'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE ON upload_sessions, assets, storage_usage TO assets_app;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO assets_app;

GRANT SELECT, INSERT, UPDATE ON upload_sessions, assets, storage_usage TO assets_system;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO assets_system;

-- +goose Down
DROP TABLE storage_usage, assets, upload_sessions;
DROP FUNCTION app_tenant_id();
