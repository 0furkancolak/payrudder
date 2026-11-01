-- The payments of the invoices lo..hi that seed-billing.sql adds: one successful charge each, and for every fifth a bank transfer with its receipt.
\set tenant '''11111111-1111-1111-1111-111111111111''::uuid'
INSERT INTO payment_ledger (tenant_id, invoice_id, provider, environment, kind, status, amount_minor, currency, provider_ref, source)
SELECT :tenant, md5('inv' || g)::uuid, 'mock', 'live', 'charge', 'succeeded', 10000 + g * 100 + (10000 + g * 100) / 5, 'EUR', 'ch_' || g, 'command' FROM generate_series(:lo, :hi) g;
WITH l AS (
  INSERT INTO payment_ledger (tenant_id, invoice_id, provider, environment, kind, status, amount_minor, currency, provider_ref, source)
  SELECT :tenant, md5('inv' || g)::uuid, 'banktransfer', 'live', 'charge', 'succeeded', 500, 'EUR', 'bt_' || g, 'manual' FROM generate_series(:lo, :hi) g WHERE g % 5 = 0
  RETURNING id, invoice_id, amount_minor, provider_ref)
INSERT INTO manual_receipts (tenant_id, invoice_id, ledger_id, bank_reference, amount_minor, currency, received_at, recorded_by)
SELECT :tenant, invoice_id, id, provider_ref, amount_minor, 'EUR', now(), 'seed' FROM l;
