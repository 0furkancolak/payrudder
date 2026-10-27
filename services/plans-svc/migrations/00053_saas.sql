-- Phase 55: Payrudder as a SaaS that bills itself (ADR 0024). One account is the platform's own: its tenant owns the plans Free, Pro and Scale, and every other
-- account is a customer of that tenant with a subscription. Nothing else changes in the schema: the customers, subscriptions, plans and entitlements are the
-- ordinary tenant tables of the platform tenant, under the same forced row-level security as any tenant.

-- +goose Up
ALTER TABLE accounts ADD COLUMN platform boolean NOT NULL DEFAULT false;
-- At most one platform account: two replicas bootstrapping at once cannot make two.
CREATE UNIQUE INDEX accounts_one_platform ON accounts ((true)) WHERE platform;

-- +goose Down
DROP INDEX accounts_one_platform;
ALTER TABLE accounts DROP COLUMN platform;
