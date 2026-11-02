-- The renewal notice (internal/contracts, renewals.go; docs/plans/21-contracts.md).

-- system. (from_time and window_time are both now.) The subscriptions Payrudder runs whose fixed fee is invoiced in advance and falls due after from_time and within the seller's notice days (7 unless the seller chose),
-- for a customer who has an e-mail address and accepted an agreement, and whose renewal has no notice yet; the soonest first. Across tenants; the job re-checks each one in its tenant's transaction
-- before it records the notice. A subscription that ends, is paused, is past due or still trialing gets none.
-- name: SystemListRenewalsDue :many
SELECT s.tenant_id, s.id, s.customer_id, s.fee_due_at::timestamptz AS renews_at
FROM subscriptions s
JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id AND c.email <> ''
LEFT JOIN contract_settings cs ON cs.tenant_id = s.tenant_id
WHERE s.state = 'active' AND upper_inf(s.active) AND NOT s.cancel_at_period_end AND s.owner = @owner::text AND s.fee_timing = 'advance'
  AND COALESCE(cs.renewal_notice_days, 7) > 0
  AND s.fee_due_at > @from_time::timestamptz AND s.fee_due_at - make_interval(0, 0, 0, COALESCE(cs.renewal_notice_days, 7)) <= @window_time::timestamptz
  AND EXISTS (SELECT 1 FROM contract_acceptances a WHERE a.tenant_id = s.tenant_id AND a.customer_id = s.customer_id)
  AND NOT EXISTS (SELECT 1 FROM contract_renewal_notices n WHERE n.tenant_id = s.tenant_id AND n.customer_id = s.customer_id AND n.renews_at = s.fee_due_at)
ORDER BY s.fee_due_at, s.id
LIMIT @batch::int;

-- Records that the customer was told of this renewal. 0 rows: they were already (another pass, another instance, the subscription this one replaced).
-- name: InsertContractRenewalNotice :execrows
INSERT INTO contract_renewal_notices (tenant_id, customer_id, renews_at, subscription_id, price_minor, currency, cancel_text, notified_at)
VALUES (app_tenant_id(), @customer_id, @renews_at, @subscription_id, @price_minor, @currency, @cancel_text, @notified_at)
ON CONFLICT (tenant_id, customer_id, renews_at) DO NOTHING;
