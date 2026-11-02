-- payments_app reads nothing (docs/plans/22 G3, "Not covered"). payments-worker connects as payments_system only (PAYRUDDER_PG_SYSTEM_DSN; the compose, Helm and Railway
-- files hold no app DSN for it), and payments_db has no row-level security, so a SELECT of payments_app would see every tenant's attempts, ledger and audit rows.
-- The role stays (the same init scripts make one per service) with the USAGE of the schema and no table.
-- +goose Up
REVOKE SELECT ON payment_attempts, provider_account_events, payment_ledger, manual_receipts, provider_events, provider_subscriptions, provider_objects,
  reconciliation_discrepancies, reconciliation_events, reconciliation_runs, settlement_lines, fee_rules, provider_account_health, provider_failover, audit_events FROM payments_app;

-- +goose Down
GRANT SELECT ON payment_attempts, provider_account_events, payment_ledger, manual_receipts, provider_events, provider_subscriptions, provider_objects,
  reconciliation_discrepancies, reconciliation_events, reconciliation_runs, settlement_lines, fee_rules, provider_account_health, provider_failover, audit_events TO payments_app;
