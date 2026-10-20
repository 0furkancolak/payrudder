-- Reports for the tenant (billing_app, RLS). The split of the revenue report is chosen by flags, not by assembling SQL.

-- name: RevenueReport :many
SELECT CASE WHEN @by_period::bool THEN i.period_id ELSE '' END::text AS period,
       CASE WHEN @by_provider::bool THEN i.provider ELSE '' END::text AS provider,
       CASE WHEN @by_country::bool THEN COALESCE(i.country, '') ELSE '' END::text AS country,
       i.currency,
       count(*) FILTER (WHERE i.status <> 'draft') AS invoices,
       COALESCE(sum(i.total_minor) FILTER (WHERE i.status IN ('open','paid','uncollectible')), 0)::bigint AS billed_minor,
       COALESCE(sum(i.total_minor) FILTER (WHERE i.status = 'paid'), 0)::bigint AS collected_minor,
       COALESCE(sum(i.total_minor) FILTER (WHERE r.invoice_id IS NOT NULL), 0)::bigint AS refunded_minor,
       COALESCE(sum(i.total_minor) FILTER (WHERE i.status = 'open'), 0)::bigint AS outstanding_minor,
       COALESCE(sum(i.total_minor) FILTER (WHERE i.status = 'uncollectible'), 0)::bigint AS uncollectible_minor,
       COALESCE(sum(i.total_minor) FILTER (WHERE i.status = 'void'), 0)::bigint AS void_minor,
       COALESCE(sum(cn.credited), 0)::bigint AS credited_minor
FROM invoices i
LEFT JOIN (SELECT invoice_id, sum(total_minor) AS credited FROM credit_notes GROUP BY invoice_id) cn ON cn.invoice_id = i.id
LEFT JOIN (SELECT DISTINCT s.invoice_id FROM invoice_sagas s JOIN saga_events e ON e.tenant_id = s.tenant_id AND e.saga_id = s.id AND e.to_state = 'REFUNDED') r
  ON r.invoice_id = i.id
WHERE i.status <> 'draft'
  AND (sqlc.narg('from_period')::text IS NULL OR i.period_id >= sqlc.narg('from_period'))
  AND (sqlc.narg('to_period')::text IS NULL OR i.period_id <= sqlc.narg('to_period'))
GROUP BY 1, 2, 3, i.currency
ORDER BY 1, 2, 3, i.currency;
