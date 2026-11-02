-- payments_app reads nothing (docs/plans/22 G3, "Not covered"): the twin of 00007 of the public set, with the shared index of charge keys.
-- +goose Up
REVOKE SELECT ON payment_attempts, provider_account_events, payment_ledger, manual_receipts, provider_events, provider_subscriptions, provider_objects,
  reconciliation_discrepancies, reconciliation_events, reconciliation_runs, settlement_lines, fee_rules, provider_account_health, provider_failover, audit_events FROM payments_app;
REVOKE SELECT ON shared.charge_keys FROM payments_app;

-- +goose Down
GRANT SELECT ON shared.charge_keys TO payments_app;
GRANT SELECT ON payment_attempts, provider_account_events, payment_ledger, manual_receipts, provider_events, provider_subscriptions, provider_objects,
  reconciliation_discrepancies, reconciliation_events, reconciliation_runs, settlement_lines, fee_rules, provider_account_health, provider_failover, audit_events TO payments_app;
