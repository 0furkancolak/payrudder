-- Accounting (billing_app, RLS): expenses the tenant records, and the income side read from invoices, their TAX lines, credit notes and the saga history.
-- Nothing here writes income; days are UTC days.

-- name: InsertExpense :one
INSERT INTO expenses (tenant_id, id, app_id, vendor, category, amount_minor, currency, incurred_on, note, recurring, recurring_until, tax_minor, attachment_asset_id)
VALUES (@tenant_id, @id, sqlc.narg('app_id'), @vendor, @category, @amount_minor, @currency, @incurred_on, @note, @recurring, sqlc.narg('recurring_until'), @tax_minor, sqlc.narg('attachment_asset_id'))
RETURNING created_at, updated_at;

-- A voided expense is history; it is not edited.
-- name: UpdateExpense :one
UPDATE expenses SET app_id = sqlc.narg('app_id'), vendor = @vendor, category = @category, amount_minor = @amount_minor, currency = @currency,
       incurred_on = @incurred_on, note = @note, recurring = @recurring, recurring_until = sqlc.narg('recurring_until'), tax_minor = @tax_minor, attachment_asset_id = sqlc.narg('attachment_asset_id'), updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id AND voided_at IS NULL
RETURNING created_at, updated_at;

-- name: VoidExpense :one
UPDATE expenses SET voided_at = now(), void_reason = @reason, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id AND voided_at IS NULL
RETURNING voided_at;

-- name: GetExpense :one
SELECT id, app_id, vendor, category, amount_minor, currency, incurred_on, note, recurring, recurring_until, voided_at, void_reason, created_at, updated_at, tax_minor, attachment_asset_id
FROM expenses WHERE tenant_id = @tenant_id AND id = @id;

-- Stored entries, newest first; a recurring entry is listed once, at its first date.
-- name: ListExpenses :many
SELECT id, app_id, vendor, category, amount_minor, currency, incurred_on, note, recurring, recurring_until, voided_at, void_reason, created_at, updated_at, tax_minor, attachment_asset_id
FROM expenses
WHERE tenant_id = @tenant_id
  AND (@include_voided::boolean OR voided_at IS NULL)
  AND (sqlc.narg('category')::text IS NULL OR category = sqlc.narg('category'))
  AND (sqlc.narg('app_id')::uuid IS NULL OR app_id = sqlc.narg('app_id'))
  AND (sqlc.narg('currency')::text IS NULL OR currency = sqlc.narg('currency'))
  AND (sqlc.narg('from_date')::date IS NULL OR incurred_on >= sqlc.narg('from_date'))
  AND (sqlc.narg('to_date')::date IS NULL OR incurred_on <= sqlc.narg('to_date'))
  AND (sqlc.narg('before_date')::date IS NULL OR (incurred_on, id) < (sqlc.narg('before_date')::date, sqlc.narg('before_id')::uuid))
ORDER BY incurred_on DESC, id DESC
LIMIT @row_limit;

-- The expenses that can have an occurrence in the window: a single one dated inside it, a recurring one that started before its end and has not ended before its start.
-- name: ExpensesInWindow :many
SELECT id, category, amount_minor, currency, incurred_on, recurring, recurring_until
FROM expenses
WHERE tenant_id = @tenant_id AND voided_at IS NULL AND incurred_on <= @to_date::date
  AND (sqlc.narg('app_id')::uuid IS NULL OR app_id = sqlc.narg('app_id'))
  AND ((recurring = 'none' AND incurred_on >= @from_date::date) OR (recurring <> 'none' AND (recurring_until IS NULL OR recurring_until >= @from_date::date)))
ORDER BY incurred_on, id
LIMIT 5000;

-- Income: an invoice counts on the day its saga reached PAID, whatever its status is now. Gross is the invoice total (tax included), tax the sum of its TAX lines.
-- The bucket is the day, or the first day of the month.
-- name: AccountingIncome :many
SELECT (CASE WHEN @monthly::boolean THEN date_trunc('month', p.paid_at AT TIME ZONE 'UTC') ELSE date_trunc('day', p.paid_at AT TIME ZONE 'UTC') END)::date AS bucket,
       i.currency::text AS currency,
       count(*)::bigint AS invoices,
       COALESCE(sum(i.total_minor), 0)::bigint AS gross_minor,
       COALESCE(sum(t.tax_minor), 0)::bigint AS tax_minor,
       COALESCE(sum(d.discount_minor), 0)::bigint AS discount_minor
FROM (
  SELECT DISTINCT ON (s.invoice_id) s.invoice_id, e.at AS paid_at
  FROM saga_events e JOIN invoice_sagas s ON s.tenant_id = e.tenant_id AND s.id = e.saga_id
  WHERE e.tenant_id = @tenant_id AND e.to_state = 'PAID' AND e.at >= @from_at::timestamptz AND e.at < @to_at::timestamptz
  ORDER BY s.invoice_id, e.at
) p
JOIN invoices i ON i.tenant_id = @tenant_id AND i.id = p.invoice_id
LEFT JOIN LATERAL (SELECT sum(l.amount_minor) AS tax_minor FROM invoice_lines l WHERE l.tenant_id = i.tenant_id AND l.invoice_id = i.id AND l.kind = 'TAX') t ON true
-- What coupons took off (contra-revenue): the DISCOUNT lines a coupon made, as a positive amount. The lines that a plan change moves back are not coupons.
LEFT JOIN LATERAL (SELECT -sum(l.amount_minor) AS discount_minor FROM invoice_lines l
                   WHERE l.tenant_id = i.tenant_id AND l.invoice_id = i.id AND l.kind = 'DISCOUNT' AND l.coupon_code IS NOT NULL) d ON true
GROUP BY 1, 2
ORDER BY 1, 2;

-- Per coupon, over the invoices paid in the window: how many, what they came to before the coupon (their positive lines except tax), what the coupon took off, per currency.
-- name: AccountingCoupons :many
SELECT i.currency::text AS currency, d.coupon_code::text AS coupon_code,
       count(*)::bigint AS invoices,
       COALESCE(sum(s.sales_minor), 0)::bigint AS sales_minor,
       COALESCE(sum(d.discount_minor), 0)::bigint AS discount_minor
FROM (
  SELECT DISTINCT ON (s.invoice_id) s.invoice_id
  FROM saga_events e JOIN invoice_sagas s ON s.tenant_id = e.tenant_id AND s.id = e.saga_id
  WHERE e.tenant_id = @tenant_id AND e.to_state = 'PAID' AND e.at >= @from_at::timestamptz AND e.at < @to_at::timestamptz
  ORDER BY s.invoice_id, e.at
) p
JOIN invoices i ON i.tenant_id = @tenant_id AND i.id = p.invoice_id
JOIN LATERAL (SELECT l.coupon_code, -sum(l.amount_minor) AS discount_minor FROM invoice_lines l
              WHERE l.tenant_id = i.tenant_id AND l.invoice_id = i.id AND l.kind = 'DISCOUNT' AND l.coupon_code IS NOT NULL GROUP BY l.coupon_code) d ON true
JOIN LATERAL (SELECT COALESCE(sum(l.amount_minor) FILTER (WHERE l.amount_minor > 0 AND l.kind <> 'TAX'), 0) AS sales_minor FROM invoice_lines l
              WHERE l.tenant_id = i.tenant_id AND l.invoice_id = i.id) s ON true
GROUP BY i.currency, d.coupon_code
ORDER BY i.currency, d.coupon_code;

-- Refunds: a credit note against an invoice that was paid, on the day it was issued; and a whole invoice refunded (a paid invoice that was voided), on the day the refund happened.
-- name: AccountingRefunds :many
SELECT (CASE WHEN @monthly::boolean THEN date_trunc('month', r.at AT TIME ZONE 'UTC') ELSE date_trunc('day', r.at AT TIME ZONE 'UTC') END)::date AS bucket,
       r.currency::text AS currency,
       count(*)::bigint AS entries,
       COALESCE(sum(r.amount_minor), 0)::bigint AS refunded_minor
FROM (
  SELECT c.created_at AS at, c.currency, c.total_minor AS amount_minor
  FROM credit_notes c
  WHERE c.tenant_id = @tenant_id AND c.created_at >= @from_at::timestamptz AND c.created_at < @to_at::timestamptz
    AND EXISTS (SELECT 1 FROM invoice_sagas s JOIN saga_events e ON e.tenant_id = s.tenant_id AND e.saga_id = s.id AND e.to_state = 'PAID'
                WHERE s.tenant_id = c.tenant_id AND s.invoice_id = c.invoice_id)
  UNION ALL
  SELECT x.at, i.currency, i.total_minor
  FROM (SELECT DISTINCT ON (s.invoice_id) s.invoice_id, e.at
        FROM saga_events e JOIN invoice_sagas s ON s.tenant_id = e.tenant_id AND s.id = e.saga_id
        WHERE e.tenant_id = @tenant_id AND e.to_state = 'REFUNDED' AND e.at >= @from_at::timestamptz AND e.at < @to_at::timestamptz
        ORDER BY s.invoice_id, e.at) x
  JOIN invoices i ON i.tenant_id = @tenant_id AND i.id = x.invoice_id
) r
GROUP BY 1, 2
ORDER BY 1, 2;

-- Dashboard: the customers whose invoices were paid in the window came to most, per currency (the same "paid" moment as the income above), at most @top_n of each.
-- name: DashboardTopCustomers :many
SELECT currency::text AS currency, customer_id, customer_external_id::text AS customer_external_id, customer_name::text AS customer_name, income_minor, invoices
FROM (
  SELECT i.currency AS currency, i.customer_id,
         COALESCE(max(i.snapshot->>'customer_external_id'), '') AS customer_external_id,
         COALESCE(max(i.snapshot->>'customer_name'), '') AS customer_name,
         COALESCE(sum(i.total_minor), 0)::bigint AS income_minor,
         count(*)::bigint AS invoices,
         row_number() OVER (PARTITION BY i.currency ORDER BY sum(i.total_minor) DESC, i.customer_id) AS rn
  FROM (
    SELECT DISTINCT ON (s.invoice_id) s.invoice_id
    FROM saga_events e JOIN invoice_sagas s ON s.tenant_id = e.tenant_id AND s.id = e.saga_id
    WHERE e.tenant_id = @tenant_id AND e.to_state = 'PAID' AND e.at >= @from_at::timestamptz AND e.at < @to_at::timestamptz
    ORDER BY s.invoice_id, e.at
  ) p
  JOIN invoices i ON i.tenant_id = @tenant_id AND i.id = p.invoice_id
  GROUP BY i.currency, i.customer_id
) t
WHERE rn <= @top_n::int
ORDER BY currency, rn;

-- Dashboard: the open invoices by how long they have been open (UTC days since they were finalized), per currency.
-- name: DashboardOpenAging :many
SELECT currency::text AS currency, bucket::text AS bucket, count(*)::bigint AS invoices, COALESCE(sum(total_minor), 0)::bigint AS amount_minor
FROM (
  SELECT x.currency, x.total_minor,
         CASE WHEN x.age <= 7 THEN '0-7' WHEN x.age <= 30 THEN '8-30' WHEN x.age <= 60 THEN '31-60' ELSE '61+' END AS bucket
  FROM (
    SELECT currency, total_minor, GREATEST(0, (now() AT TIME ZONE 'UTC')::date - (COALESCE(finalized_at, created_at) AT TIME ZONE 'UTC')::date) AS age
    FROM invoices WHERE tenant_id = @tenant_id AND status = 'open'
  ) x
) y
GROUP BY currency, bucket
ORDER BY currency, bucket;

-- Dashboard: the newest invoices, for the activity feed (the primary key is (tenant, id) and ids are time ordered, so this reads the tail of the index).
-- name: DashboardRecentInvoices :many
SELECT id, COALESCE(number, '')::text AS number, COALESCE(snapshot->>'customer_external_id', '')::text AS customer_external_id, status::text AS status, currency::text AS currency,
       total_minor, created_at
FROM invoices WHERE tenant_id = @tenant_id
ORDER BY id DESC
LIMIT 8;
