-- Corrections to usage events (ADR 0027, docs/plans/22 G12), for a tenant's schema: the same as ../00002_event_corrections.sql.
--
-- A correction names the idempotency key of the event it corrects, and ingest-svc has to know what that event was: its customer, its meter, its time (the period the
-- correction lands in) and its quantity (what the correction is a difference from). The key row already says "this key was an event"; four columns say which. They are
-- NULL for the keys written before this migration, which cannot be corrected (the window of ADR 0014 is 76 days at most, so that passes by itself).
--
-- event_corrections is the record of each correction: the correcting event, the original, the difference the correction published, the reason and who made it. The running
-- difference of an original (what corrections have moved it by) is the sum over it, read under an advisory lock on the original so that two corrections of one event see each
-- other. It is kept for good (it is the audit trail, a row per correction); it is keyed by event id, not by the idempotency key, because a key can be used again once its
-- month's partition is gone.
-- +goose Up
ALTER TABLE idempotency_keys
  ADD COLUMN customer_id    uuid,
  ADD COLUMN meter_code     text,
  ADD COLUMN event_time     timestamptz,
  ADD COLUMN quantity_units bigint;

CREATE TABLE event_corrections (
  tenant_id          uuid        NOT NULL,
  event_id           text        NOT NULL,
  correction_key     text        NOT NULL,
  corrects_key       text        NOT NULL,
  corrects_event_id  text        NOT NULL,
  customer_id        uuid        NOT NULL,
  meter_code         text        NOT NULL,
  delta_units        bigint      NOT NULL,
  reason             text        NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
  corrected_by       text        NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, event_id)
);
CREATE INDEX event_corrections_of ON event_corrections (tenant_id, corrects_event_id);

ALTER TABLE event_corrections ENABLE ROW LEVEL SECURITY;
ALTER TABLE event_corrections FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON event_corrections
  USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

GRANT SELECT, INSERT ON event_corrections TO ingest_app;
-- Resetting a sandbox deletes the tenant's corrections with its dedup keys.
GRANT SELECT, DELETE ON event_corrections TO ingest_system;

-- +goose Down
DROP TABLE event_corrections;
ALTER TABLE idempotency_keys DROP COLUMN quantity_units, DROP COLUMN event_time, DROP COLUMN meter_code, DROP COLUMN customer_id;
