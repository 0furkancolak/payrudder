-- Phase 41: the dashboard's open-invoice ageing reads the open invoices of a tenant; a partial index keeps that read to the open ones however many invoices are paid.
-- +goose Up
CREATE INDEX invoices_open ON invoices (tenant_id, finalized_at) WHERE status = 'open';

-- +goose Down
DROP INDEX invoices_open;
