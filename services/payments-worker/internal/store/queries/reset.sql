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

-- The ledger and the receipts refuse every DELETE (migration 00002) unless the transaction names the tenant as being reset. ResetLiveRowCount is asked first, before
-- ResetBegin: the caller's word that a tenant is a sandbox is not the database's, and no row of the live environment is ever deleted.
-- name: ResetLiveRowCount :one
SELECT count(*) FROM (
  SELECT 1 FROM payment_ledger l WHERE l.tenant_id = $1 AND l.environment <> 'sandbox'
  UNION ALL
  SELECT 1 FROM payment_attempts a WHERE a.tenant_id = $1 AND a.environment <> 'sandbox'
) live;
-- name: ResetBegin :exec
SELECT set_config('payrudder.sandbox_reset', $1, true);
