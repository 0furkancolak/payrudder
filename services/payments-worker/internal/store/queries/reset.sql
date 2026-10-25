-- Resetting a sandbox tenant (phase 45 E): the attempts, the ledger and what hangs on them go; the tenant's provider accounts (credentials, defaults) and the
-- objects created at providers stay. Run as payments_system from internal/sandboxreset only; every statement names the tenant.

-- name: ResetManualReceipts :execrows
DELETE FROM manual_receipts WHERE tenant_id = $1;
-- name: ResetPaymentLedger :execrows
DELETE FROM payment_ledger WHERE tenant_id = $1;
-- name: ResetPaymentAttempts :execrows
DELETE FROM payment_attempts WHERE tenant_id = $1;
-- name: ResetProviderSubscriptions :execrows
DELETE FROM provider_subscriptions WHERE tenant_id = $1;
-- name: ResetProviderEvents :execrows
DELETE FROM provider_events WHERE tenant_id = $1;
-- name: ResetEInvoiceIssuances :execrows
DELETE FROM einvoice_issuances WHERE tenant_id = $1;
