-- Settlement: what the provider (or the bank) says it settled, next to what the ledger says was paid (docs/plans/22 G7). Every statement names the tenant.

-- name: UpsertSettlementLine :execrows
-- A later read of a line fills in what was unknown (the payout it went out in, when funds became available) and never takes it away.
INSERT INTO settlement_lines (tenant_id, environment, provider, line_ref, kind, payment_ref, currency, gross_minor, fee_minor, net_minor, occurred_at, available_on, settled_at, batch, source)
VALUES (@tenant_id, @environment, @provider, @line_ref, @kind, @payment_ref, @currency, @gross_minor, @fee_minor, @net_minor, @occurred_at, @available_on, @settled_at, @batch, @source)
ON CONFLICT (tenant_id, provider, line_ref) DO UPDATE SET
  currency = EXCLUDED.currency, gross_minor = EXCLUDED.gross_minor, fee_minor = EXCLUDED.fee_minor, net_minor = EXCLUDED.net_minor,
  available_on = COALESCE(EXCLUDED.available_on, settlement_lines.available_on),
  settled_at = COALESCE(EXCLUDED.settled_at, settlement_lines.settled_at),
  batch = CASE WHEN EXCLUDED.batch <> '' THEN EXCLUDED.batch ELSE settlement_lines.batch END
WHERE (settlement_lines.currency, settlement_lines.gross_minor, settlement_lines.fee_minor, settlement_lines.net_minor, settlement_lines.available_on, settlement_lines.settled_at, settlement_lines.batch)
  IS DISTINCT FROM (EXCLUDED.currency, EXCLUDED.gross_minor, EXCLUDED.fee_minor, EXCLUDED.net_minor, COALESCE(EXCLUDED.available_on, settlement_lines.available_on),
  COALESCE(EXCLUDED.settled_at, settlement_lines.settled_at), CASE WHEN EXCLUDED.batch <> '' THEN EXCLUDED.batch ELSE settlement_lines.batch END);

-- name: ListSettlementLines :many
SELECT id, provider, line_ref, kind, payment_ref, currency, gross_minor, fee_minor, net_minor, occurred_at, available_on, settled_at, batch, source
FROM settlement_lines WHERE tenant_id = @tenant_id AND occurred_at >= @since::timestamptz ORDER BY id;

-- name: UpsertFeeRule :exec
INSERT INTO fee_rules (tenant_id, environment, provider, currency, rate_bps, fixed_minor)
VALUES (@tenant_id, @environment, @provider, @currency, @rate_bps, @fixed_minor)
ON CONFLICT (tenant_id, environment, provider, currency) DO UPDATE SET rate_bps = EXCLUDED.rate_bps, fixed_minor = EXCLUDED.fixed_minor, updated_at = now();

-- name: DeleteFeeRule :execrows
DELETE FROM fee_rules WHERE tenant_id = @tenant_id AND environment = @environment AND provider = @provider AND currency = @currency;

-- name: ListFeeRules :many
SELECT environment, provider, currency, rate_bps, fixed_minor, updated_at FROM fee_rules WHERE tenant_id = @tenant_id ORDER BY provider, currency;
