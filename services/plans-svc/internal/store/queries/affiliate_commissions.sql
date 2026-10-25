-- Commissions and their reversals (phase 47). Tenant statements name the tenant through app_tenant_id(); the one marked "system" runs as plans_system across tenants.

-- name: CommissionHistory :one
-- What a customer has earned their affiliate before: how many commissions (a reversed one counts) and when the first was earned (the epoch when there is none).
-- The program's term reads this.
SELECT count(*)::bigint AS prior, COALESCE(min(earned_at), 'epoch'::timestamptz)::timestamptz AS first_earned
FROM affiliate_commissions WHERE tenant_id = app_tenant_id() AND customer_id = $1 AND kind = 'commission';

-- name: InsertCommission :execrows
-- One commission per paid invoice: a replay of invoice.paid inserts nothing.
INSERT INTO affiliate_commissions (tenant_id, affiliate_id, customer_id, invoice_id, invoice_number, currency, invoice_total_minor, net_minor, amount_minor, status,
  earned_at, approve_after, approved_at)
VALUES (app_tenant_id(), @affiliate_id, @customer_id, @invoice_id, @invoice_number, @currency, @invoice_total_minor, @net_minor, @amount_minor, @status,
  @earned_at, @approve_after, CASE WHEN @status::text = 'approved' THEN @earned_at::timestamptz END)
ON CONFLICT (tenant_id, invoice_id) WHERE kind = 'commission' DO NOTHING;

-- name: LockCommissionOfInvoice :one
-- The commission of an invoice, locked: reversals of one commission are applied one after the other.
SELECT id, affiliate_id, customer_id, currency, invoice_total_minor, amount_minor, reversed_minor, status, payout_id
FROM affiliate_commissions WHERE tenant_id = app_tenant_id() AND invoice_id = @invoice_id AND kind = 'commission' FOR UPDATE;

-- name: GetReversal :one
SELECT amount_minor FROM affiliate_reversals WHERE tenant_id = app_tenant_id() AND key = $1;

-- name: SumCreditedOfCommission :one
-- How much of the invoice has been credited back so far, by the credit notes seen.
SELECT COALESCE(sum(credited_minor), 0)::bigint AS credited FROM affiliate_reversals
WHERE tenant_id = app_tenant_id() AND commission_id = $1 AND kind = 'credit_note';

-- name: InsertReversal :exec
INSERT INTO affiliate_reversals (tenant_id, key, commission_id, invoice_id, kind, credited_minor, amount_minor, note)
VALUES (app_tenant_id(), @key, @commission_id, @invoice_id, @kind, @credited_minor, @amount_minor, @note);

-- name: TakeBackFromUnpaidCommission :exec
-- A commission that has not been paid loses what was reversed; when nothing is left it is `reversed`.
UPDATE affiliate_commissions SET reversed_minor = reversed_minor + @delta::bigint,
  status = CASE WHEN reversed_minor + @delta::bigint >= amount_minor THEN 'reversed' ELSE status END
WHERE tenant_id = app_tenant_id() AND id = @id AND kind = 'commission';

-- name: NoteReversalOfPaidCommission :exec
UPDATE affiliate_commissions SET reversed_minor = reversed_minor + @delta::bigint WHERE tenant_id = app_tenant_id() AND id = @id AND kind = 'commission';

-- name: InsertReversalRow :exec
-- A commission already paid is taken back by a negative row of its own, approved at once, that the next payout nets.
INSERT INTO affiliate_commissions (tenant_id, affiliate_id, customer_id, kind, invoice_id, invoice_number, currency, invoice_total_minor, net_minor, amount_minor, status,
  parent_id, reversal_key, earned_at, approve_after, approved_at)
SELECT p.tenant_id, p.affiliate_id, p.customer_id, 'reversal', p.invoice_id, p.invoice_number, p.currency, p.invoice_total_minor, p.net_minor, 0 - @delta::bigint, 'approved',
  p.id, @key::text, @at::timestamptz, @at::timestamptz, @at::timestamptz
FROM affiliate_commissions p WHERE p.tenant_id = app_tenant_id() AND p.id = @parent_id;

-- name: SystemApproveDueCommissions :execrows
-- system. The hold is over: a pending commission becomes approved. Idempotent, so any instance may run it at any time.
UPDATE affiliate_commissions SET status = 'approved', approved_at = @now::timestamptz
WHERE status = 'pending' AND kind = 'commission' AND approve_after <= @now::timestamptz;

-- name: ListAffiliateCommissions :many
SELECT c.id, c.affiliate_id, a.code AS affiliate_code, cu.external_id AS customer_external_id, c.kind, c.invoice_id, c.invoice_number, c.currency, c.invoice_total_minor,
  c.net_minor, c.amount_minor, c.reversed_minor, c.status, c.parent_id, c.earned_at, c.approve_after, c.approved_at, c.paid_at, c.payout_id, count(*) OVER()::bigint AS _count
FROM affiliate_commissions c
JOIN affiliates a ON a.tenant_id = c.tenant_id AND a.id = c.affiliate_id
JOIN customers cu ON cu.tenant_id = c.tenant_id AND cu.id = c.customer_id
WHERE c.tenant_id = app_tenant_id()
  AND (sqlc.narg('affiliate_id')::uuid IS NULL OR c.affiliate_id = sqlc.narg('affiliate_id'))
  AND (sqlc.narg('status')::text IS NULL OR c.status = sqlc.narg('status'))
  AND (sqlc.narg('kind')::text IS NULL OR c.kind = sqlc.narg('kind'))
  AND (sqlc.narg('invoice_id')::uuid IS NULL OR c.invoice_id = sqlc.narg('invoice_id'))
  AND (sqlc.narg('payout_id')::uuid IS NULL OR c.payout_id = sqlc.narg('payout_id'))
  AND (sqlc.narg('from_time')::timestamptz IS NULL OR c.earned_at >= sqlc.narg('from_time'))
  AND (sqlc.narg('to_time')::timestamptz IS NULL OR c.earned_at < sqlc.narg('to_time'))
ORDER BY c.earned_at DESC, c.created_at DESC, c.id
LIMIT @row_limit OFFSET @row_offset;

-- name: AffiliateBalances :many
-- What each affiliate has, per currency: waiting out the hold, available for a payout, paid out (net of what was netted), and taken back in all.
SELECT affiliate_id, currency,
  COALESCE(sum(amount_minor - reversed_minor) FILTER (WHERE kind = 'commission' AND status = 'pending'), 0)::bigint AS pending_minor,
  COALESCE(sum(CASE kind WHEN 'commission' THEN amount_minor - reversed_minor ELSE amount_minor END) FILTER (WHERE status = 'approved'), 0)::bigint AS approved_minor,
  COALESCE(sum(settled_minor) FILTER (WHERE status = 'paid'), 0)::bigint AS paid_minor,
  COALESCE(sum(reversed_minor) FILTER (WHERE kind = 'commission'), 0)::bigint AS reversed_minor
FROM affiliate_commissions
WHERE tenant_id = app_tenant_id() AND (sqlc.narg('affiliate_id')::uuid IS NULL OR affiliate_id = sqlc.narg('affiliate_id'))
GROUP BY affiliate_id, currency ORDER BY affiliate_id, currency;

-- name: AffiliateFunnelClicks :one
SELECT COALESCE(sum(clicks), 0)::bigint AS clicks FROM affiliate_clicks_daily
WHERE tenant_id = app_tenant_id() AND day >= @from_day::date AND day <= @to_day::date AND (sqlc.narg('affiliate_id')::uuid IS NULL OR affiliate_id = sqlc.narg('affiliate_id'));

-- name: AffiliateFunnelReferrals :one
SELECT count(*)::bigint AS referrals FROM affiliate_attributions
WHERE tenant_id = app_tenant_id() AND attributed_at >= @from_time::timestamptz AND attributed_at < @to_time::timestamptz
  AND (sqlc.narg('affiliate_id')::uuid IS NULL OR affiliate_id = sqlc.narg('affiliate_id'));

-- name: AffiliateFunnelPaying :one
SELECT count(DISTINCT customer_id)::bigint AS paying FROM affiliate_commissions
WHERE tenant_id = app_tenant_id() AND kind = 'commission' AND earned_at >= @from_time::timestamptz AND earned_at < @to_time::timestamptz
  AND (sqlc.narg('affiliate_id')::uuid IS NULL OR affiliate_id = sqlc.narg('affiliate_id'));

-- name: AffiliateFunnelCommission :many
SELECT currency, COALESCE(sum(amount_minor), 0)::bigint AS earned_minor, COALESCE(sum(reversed_minor), 0)::bigint AS reversed_minor
FROM affiliate_commissions
WHERE tenant_id = app_tenant_id() AND kind = 'commission' AND earned_at >= @from_time::timestamptz AND earned_at < @to_time::timestamptz
  AND (sqlc.narg('affiliate_id')::uuid IS NULL OR affiliate_id = sqlc.narg('affiliate_id'))
GROUP BY currency ORDER BY currency;
