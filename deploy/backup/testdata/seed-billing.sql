-- Test data is loaded as the superuser with the immutability triggers off (invoices are seeded finished, with their lines).
SET session_replication_role = replica;
-- Invoices lo..hi of one tenant with their lines (base fee, usage, 20% tax), a credit lot each and its ledger (a grant and a consume): psql -v lo=1 -v hi=10 -f seed-billing.sql.
-- Ids come from the number, so another range adds new rows and the same range fails on the primary key. The totals are the ones invariants.sh expects.
\set tenant '''11111111-1111-1111-1111-111111111111''::uuid'
INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, number, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, finalized_at)
SELECT :tenant, md5('inv' || g)::uuid, md5('cus' || g)::uuid, '2026-10', 'REGULAR', 'INV-2026-' || lpad(g::text, 6, '0'), md5('pv')::uuid, 'EUR',
       10000 + g * 100, 10000 + g * 100 + (10000 + g * 100) / 5, 'paid', '{"environment":"live"}', now()
FROM generate_series(:lo, :hi) g;
INSERT INTO invoice_lines (tenant_id, invoice_id, line_no, kind, amount_minor, description)
SELECT :tenant, md5('inv' || g)::uuid, 1, 'BASE_FEE', 10000, 'Base fee' FROM generate_series(:lo, :hi) g
UNION ALL SELECT :tenant, md5('inv' || g)::uuid, 2, 'USAGE', g * 100, 'Usage' FROM generate_series(:lo, :hi) g
UNION ALL SELECT :tenant, md5('inv' || g)::uuid, 3, 'TAX', (10000 + g * 100) / 5, 'VAT 20%' FROM generate_series(:lo, :hi) g;
INSERT INTO credit_lots (tenant_id, id, customer_id, granted, remaining, source)
SELECT :tenant, md5('lot' || g)::uuid, md5('cus' || g)::uuid, 100, 70, 'promo' FROM generate_series(:lo, :hi) g;
INSERT INTO credit_ledger (tenant_id, id, customer_id, lot_id, kind, amount, reason)
SELECT :tenant, md5('grant' || g)::uuid, md5('cus' || g)::uuid, md5('lot' || g)::uuid, 'grant', 100, 'seed' FROM generate_series(:lo, :hi) g
UNION ALL SELECT :tenant, md5('use' || g)::uuid, md5('cus' || g)::uuid, md5('lot' || g)::uuid, 'consume', -30, 'seed' FROM generate_series(:lo, :hi) g;
