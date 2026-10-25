-- Payouts and the journal (phase 47). Every statement names the tenant through app_tenant_id(); RLS binds it as well.

-- name: LockPayableCommissions :many
-- What can go into a payout: approved, in no payout yet, of an active affiliate; locked until the transaction ends so that two payouts cannot take the same row. A reversal
-- that is applying to one of these waits for the lock, or is waited for.
SELECT c.id, c.affiliate_id, c.currency, c.kind,
  (CASE c.kind WHEN 'commission' THEN c.amount_minor - c.reversed_minor ELSE c.amount_minor END)::bigint AS effective_minor
FROM affiliate_commissions c JOIN affiliates a ON a.tenant_id = c.tenant_id AND a.id = c.affiliate_id
WHERE c.tenant_id = app_tenant_id() AND c.status = 'approved' AND c.payout_id IS NULL AND a.status = 'active'
  AND (sqlc.narg('affiliate_id')::uuid IS NULL OR c.affiliate_id = sqlc.narg('affiliate_id'))
  AND (sqlc.narg('currency')::text IS NULL OR c.currency = sqlc.narg('currency'))
ORDER BY c.affiliate_id, c.currency, c.earned_at, c.id
FOR UPDATE OF c;

-- name: InsertAffiliatePayout :one
INSERT INTO affiliate_payouts (tenant_id, affiliate_id, currency, total_minor, commission_count, method, payout_details, sandbox)
VALUES (app_tenant_id(), @affiliate_id, @currency, @total_minor, @commission_count, @method, @payout_details, @sandbox)
RETURNING id;

-- name: GetOpenAffiliatePayout :one
-- The open payout of an affiliate in a currency, if there is one: what is approved later joins it instead of making a second.
SELECT id FROM affiliate_payouts WHERE tenant_id = app_tenant_id() AND affiliate_id = @affiliate_id AND currency = @currency AND status = 'open' FOR UPDATE;

-- name: AssignCommissionsToPayout :execrows
UPDATE affiliate_commissions SET payout_id = @payout_id WHERE tenant_id = app_tenant_id() AND id = ANY(@ids::uuid[]) AND status = 'approved' AND payout_id IS NULL;

-- name: LockAffiliatePayout :one
SELECT id, affiliate_id, currency, method, status, sandbox FROM affiliate_payouts WHERE tenant_id = app_tenant_id() AND id = $1 FOR UPDATE;

-- name: LockPayoutRows :many
-- The rows of a payout, locked before it is totalled and paid: a reversal that is applying to one of them finishes first, so the total and what each row is settled at agree.
SELECT id FROM affiliate_commissions WHERE tenant_id = app_tenant_id() AND payout_id = $1 AND status = 'approved' ORDER BY id FOR UPDATE;

-- name: SumPayoutRows :one
-- What an open payout is worth now: its approved rows at their effective amounts (a row reversed in full since is out).
SELECT COALESCE(sum(CASE kind WHEN 'commission' THEN amount_minor - reversed_minor ELSE amount_minor END), 0)::bigint AS total_minor, count(*)::int AS rows_count
FROM affiliate_commissions WHERE tenant_id = app_tenant_id() AND payout_id = $1 AND status = 'approved';

-- name: MarkPayoutCommissionsPaid :execrows
UPDATE affiliate_commissions SET status = 'paid', paid_at = @now::timestamptz,
  settled_minor = CASE kind WHEN 'commission' THEN amount_minor - reversed_minor ELSE amount_minor END
WHERE tenant_id = app_tenant_id() AND payout_id = @payout_id AND status = 'approved';

-- name: MarkAffiliatePayoutPaid :exec
UPDATE affiliate_payouts SET status = 'paid', paid_at = @now::timestamptz, total_minor = @total_minor, commission_count = @commission_count, reference = @reference
WHERE tenant_id = app_tenant_id() AND id = @id;

-- name: CancelAffiliatePayout :exec
UPDATE affiliate_payouts SET status = 'cancelled', cancelled_at = @now::timestamptz WHERE tenant_id = app_tenant_id() AND id = @id;

-- name: ReleasePayoutCommissions :exec
UPDATE affiliate_commissions SET payout_id = NULL WHERE tenant_id = app_tenant_id() AND payout_id = $1 AND status <> 'paid';

-- name: ListAffiliatePayouts :many
SELECT p.id, p.affiliate_id, a.code AS affiliate_code, p.currency, p.method, p.payout_details, p.reference, p.status, p.sandbox, p.created_at, p.paid_at, p.cancelled_at,
  CASE p.status WHEN 'open' THEN live.total_minor ELSE p.total_minor END::bigint AS total_minor,
  CASE p.status WHEN 'open' THEN live.rows_count ELSE p.commission_count END::int AS commission_count,
  count(*) OVER()::bigint AS _count
FROM affiliate_payouts p
JOIN affiliates a ON a.tenant_id = p.tenant_id AND a.id = p.affiliate_id
LEFT JOIN LATERAL (
  SELECT COALESCE(sum(CASE c.kind WHEN 'commission' THEN c.amount_minor - c.reversed_minor ELSE c.amount_minor END), 0)::bigint AS total_minor, count(*)::int AS rows_count
  FROM affiliate_commissions c WHERE c.tenant_id = p.tenant_id AND c.payout_id = p.id AND c.status = 'approved'
) live ON true
WHERE p.tenant_id = app_tenant_id()
  AND (sqlc.narg('affiliate_id')::uuid IS NULL OR p.affiliate_id = sqlc.narg('affiliate_id'))
  AND (sqlc.narg('status')::text IS NULL OR p.status = sqlc.narg('status'))
  AND (sqlc.narg('payout_id')::uuid IS NULL OR p.id = sqlc.narg('payout_id'))
ORDER BY p.created_at DESC, p.id
LIMIT @row_limit OFFSET @row_offset;

-- name: InsertJournalLine :exec
INSERT INTO affiliate_journal (tenant_id, payout_id, affiliate_id, posted_at, currency, account, debit_minor, credit_minor, memo, sandbox)
VALUES (app_tenant_id(), @payout_id, @affiliate_id, @posted_at, @currency, @account, @debit_minor, @credit_minor, @memo, @sandbox);

-- name: ListAffiliateJournal :many
SELECT j.id, j.payout_id, j.affiliate_id, j.posted_at, j.currency, j.account, j.debit_minor, j.credit_minor, j.memo, j.sandbox, count(*) OVER()::bigint AS _count
FROM affiliate_journal j
WHERE j.tenant_id = app_tenant_id()
  AND (sqlc.narg('payout_id')::uuid IS NULL OR j.payout_id = sqlc.narg('payout_id'))
  AND (sqlc.narg('currency')::text IS NULL OR j.currency = sqlc.narg('currency'))
  AND (sqlc.narg('from_time')::timestamptz IS NULL OR j.posted_at >= sqlc.narg('from_time'))
  AND (sqlc.narg('to_time')::timestamptz IS NULL OR j.posted_at < sqlc.narg('to_time'))
ORDER BY j.posted_at DESC, j.account, j.id
LIMIT @row_limit OFFSET @row_offset;

-- name: AffiliateJournalTotals :many
-- Debits and credits per currency over the same filter: the books balance when they are equal in every currency.
SELECT currency, COALESCE(sum(debit_minor), 0)::bigint AS debit_minor, COALESCE(sum(credit_minor), 0)::bigint AS credit_minor
FROM affiliate_journal
WHERE tenant_id = app_tenant_id()
  AND (sqlc.narg('payout_id')::uuid IS NULL OR payout_id = sqlc.narg('payout_id'))
  AND (sqlc.narg('currency')::text IS NULL OR currency = sqlc.narg('currency'))
  AND (sqlc.narg('from_time')::timestamptz IS NULL OR posted_at >= sqlc.narg('from_time'))
  AND (sqlc.narg('to_time')::timestamptz IS NULL OR posted_at < sqlc.narg('to_time'))
GROUP BY currency ORDER BY currency;
