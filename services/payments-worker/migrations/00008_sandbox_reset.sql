-- Phase 45 E: resetting a sandbox deletes a tenant's attempts, ledger rows and what hangs on them. The ledger is append-only for the product (no role of the request
-- path can delete from it); only payments_system, and only the reset, does.

-- +goose Up
GRANT DELETE ON manual_receipts, payment_ledger, payment_attempts, provider_subscriptions, einvoice_issuances TO payments_system;

-- +goose Down
REVOKE DELETE ON manual_receipts, payment_ledger, payment_attempts, provider_subscriptions, einvoice_issuances FROM payments_system;
