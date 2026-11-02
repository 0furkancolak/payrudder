-- Resetting a sandbox tenant (phase 45 E): the attempts, the ledger and what hangs on them go; the tenant's provider accounts (credentials, defaults) and the
-- objects created at providers stay. Run as payments_system from internal/sandboxreset only; every statement names the tenant.

-- name: ResetManualReceipts :execrows
DELETE FROM manual_receipts WHERE tenant_id = $1;
-- name: ResetPaymentLedger :execrows
DELETE FROM payment_ledger WHERE tenant_id = $1;
-- name: ResetPaymentDisputeEvents :execrows
DELETE FROM payment_dispute_events WHERE tenant_id = $1;
-- name: ResetPaymentDisputes :execrows
DELETE FROM payment_disputes WHERE tenant_id = $1;
-- name: ResetPaymentAttempts :execrows
DELETE FROM payment_attempts WHERE tenant_id = $1;
-- name: ResetProviderSubscriptions :execrows
DELETE FROM provider_subscriptions WHERE tenant_id = $1;
-- name: ResetProviderEvents :execrows
DELETE FROM provider_events WHERE tenant_id = $1;
-- name: ResetEInvoiceIssuances :execrows
DELETE FROM einvoice_issuances WHERE tenant_id = $1;

-- name: ResetReconciliationEvents :execrows
DELETE FROM reconciliation_events WHERE tenant_id = $1;
-- name: ResetReconciliationRuns :execrows
DELETE FROM reconciliation_runs WHERE tenant_id = $1;
-- name: ResetReconciliationDiscrepancies :execrows
DELETE FROM reconciliation_discrepancies WHERE tenant_id = $1;
-- name: ResetSettlementLines :execrows
DELETE FROM settlement_lines WHERE tenant_id = $1;

-- The ledger and the receipts refuse every DELETE (migration 00002) unless the transaction names the tenant as being reset. ResetLiveRowCount is asked first, before
-- ResetBegin: the caller's word that a tenant is a sandbox is not the database's, and no row of the live environment is ever deleted. Every table the reset deletes from is asked about.
-- name: ResetLiveRowCount :one
SELECT count(*) FROM (
  SELECT 1 FROM payment_ledger l WHERE l.tenant_id = $1 AND l.environment <> 'sandbox'
  UNION ALL
  SELECT 1 FROM payment_attempts a WHERE a.tenant_id = $1 AND a.environment <> 'sandbox'
  UNION ALL
  SELECT 1 FROM reconciliation_discrepancies d WHERE d.tenant_id = $1 AND d.environment <> 'sandbox'
  UNION ALL
  SELECT 1 FROM settlement_lines s WHERE s.tenant_id = $1 AND s.environment <> 'sandbox'
  UNION ALL
  SELECT 1 FROM einvoice_issuances i WHERE i.tenant_id = $1 AND i.environment <> 'sandbox'
  UNION ALL
  SELECT 1 FROM provider_subscriptions p WHERE p.tenant_id = $1 AND p.environment <> 'sandbox'
  -- A notification has no environment of its own. A tenant that has a live provider account is a live tenant (a sandbox tenant's accounts are stored for the sandbox), and the
  -- notifications it has received are live data.
  UNION ALL
  SELECT 1 FROM provider_events e WHERE e.tenant_id = $1 AND EXISTS (SELECT 1 FROM provider_accounts a WHERE a.tenant_id = e.tenant_id AND a.environment <> 'sandbox')
) live;
-- name: ResetBegin :exec
SELECT set_config('payrudder.sandbox_reset', $1, true);
