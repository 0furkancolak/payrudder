-- The per-tenant message log (plan 11): a tenant's recent sends, newest first, a page at a time (mail without a tenant, the platform's own, is not in it), with what the
-- provider reported of each: delivered, delayed, bounced (for good), soft_bounced or complained. It is the history of the mail, so lifting a suppression does not change it.

-- +goose Up
ALTER TABLE sent_notifications ADD COLUMN delivery_state text NOT NULL DEFAULT '' CHECK (delivery_state IN ('', 'delivered', 'delayed', 'bounced', 'soft_bounced', 'complained'));
CREATE INDEX sent_notifications_by_tenant ON sent_notifications (tenant_id, claimed_at DESC, dedup_key DESC) WHERE tenant_id IS NOT NULL;

-- +goose Down
DROP INDEX sent_notifications_by_tenant;
ALTER TABLE sent_notifications DROP COLUMN delivery_state;
