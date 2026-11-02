-- Price changes (plan 15): the record of a run of SchedulePriceChange and what happened to each subscription. The state of a scheduled item is the state of its
-- subscription_changes row (pending = scheduled, applied, cancelled = withdrawn), so it is joined, never copied.

-- A run continues the open price change of the same plan and versions, else makes one. effective_at is only the end of the notice until the first renewal is scheduled.
-- effective_from is the date the seller chose (null: each subscription's next renewal); a run that continues an open price change keeps the date it has, and the caller
-- refuses a run that names another.
-- name: UpsertOpenPriceChange :one
INSERT INTO price_changes (tenant_id, id, plan_code, from_version, to_version, notice_days, effective_at, effective_from, created_by)
VALUES (app_tenant_id(), @id, @plan_code, @from_version, @to_version, @notice_days, @effective_at, sqlc.narg('effective_from')::timestamptz, @created_by)
ON CONFLICT (tenant_id, plan_code, from_version, to_version) WHERE state = 'open' DO UPDATE SET notice_days = EXCLUDED.notice_days
RETURNING id, effective_from;

-- name: GetOpenPriceChangeID :one
SELECT id FROM price_changes
WHERE tenant_id = app_tenant_id() AND plan_code = @plan_code AND from_version = @from_version AND to_version = @to_version AND state = 'open';

-- What a run did to one subscription: scheduled (change_id and the date of the renewal) or skipped (reason). A later run replaces a skip.
-- name: UpsertPriceChangeItem :exec
INSERT INTO price_change_items (tenant_id, price_change_id, subscription_id, change_id, reason, effective_at, notified_at)
VALUES (app_tenant_id(), @price_change_id, @subscription_id, sqlc.narg('change_id')::uuid, @reason, sqlc.narg('effective_at')::timestamptz, sqlc.narg('notified_at')::timestamptz)
ON CONFLICT (tenant_id, price_change_id, subscription_id)
DO UPDATE SET change_id = EXCLUDED.change_id, reason = EXCLUDED.reason, effective_at = EXCLUDED.effective_at, notified_at = EXCLUDED.notified_at, updated_at = now();

-- The change an item carries and its state ('' when the item is a skip). No row: the price change has not met this subscription.
-- name: GetPriceChangeItemChange :one
SELECT i.change_id, COALESCE(c.state, '')::text AS change_state
FROM price_change_items i
LEFT JOIN subscription_changes c ON c.tenant_id = i.tenant_id AND c.id = i.change_id
WHERE i.tenant_id = app_tenant_id() AND i.price_change_id = @price_change_id AND i.subscription_id = @subscription_id;

-- The earliest date a price changes, once there is one.
-- name: SetPriceChangeEffective :exec
UPDATE price_changes SET effective_at = COALESCE((SELECT min(i.effective_at) FROM price_change_items i WHERE i.tenant_id = price_changes.tenant_id AND i.price_change_id = price_changes.id), effective_at)
WHERE tenant_id = app_tenant_id() AND id = @id;

-- Newest first, with the number of subscriptions in each state; one by id, or the ones of a plan.
-- name: ListPriceChanges :many
SELECT pc.id, pc.plan_code, pc.from_version, pc.to_version, pc.notice_days, pc.effective_at, pc.effective_from, pc.state, pc.created_by, pc.created_at, pc.withdrawn_at, pc.withdrawn_by,
       COALESCE(n.scheduled, 0)::int AS scheduled, COALESCE(n.applied, 0)::int AS applied, COALESCE(n.withdrawn, 0)::int AS withdrawn, COALESCE(n.skipped, 0)::int AS skipped
FROM price_changes pc
LEFT JOIN LATERAL (
  SELECT count(*) FILTER (WHERE c.state = 'pending') AS scheduled, count(*) FILTER (WHERE c.state = 'applied') AS applied,
         count(*) FILTER (WHERE c.state = 'cancelled') AS withdrawn, count(*) FILTER (WHERE i.change_id IS NULL) AS skipped
  FROM price_change_items i
  LEFT JOIN subscription_changes c ON c.tenant_id = i.tenant_id AND c.id = i.change_id
  WHERE i.tenant_id = pc.tenant_id AND i.price_change_id = pc.id
) n ON true
WHERE pc.tenant_id = app_tenant_id() AND (sqlc.narg('id')::uuid IS NULL OR pc.id = sqlc.narg('id')::uuid) AND (@plan_code::text = '' OR pc.plan_code = @plan_code::text)
ORDER BY pc.created_at DESC, pc.id DESC
LIMIT 100;

-- The subscriptions of a price change, by customer, a page at a time (the keyset is the customer's external id and the subscription); state: skipped, scheduled,
-- applied or withdrawn (empty: all). The caller asks for one row more than the page, to know whether there is a next page.
-- name: ListPriceChangeItems :many
SELECT * FROM (
  SELECT i.subscription_id, cu.external_id AS customer_external_id,
         (CASE WHEN i.change_id IS NULL THEN 'skipped' WHEN c.state = 'pending' THEN 'scheduled' WHEN c.state = 'applied' THEN 'applied' ELSE 'withdrawn' END)::text AS state,
         i.reason, i.effective_at, i.notified_at
  FROM price_change_items i
  JOIN subscriptions s ON s.tenant_id = i.tenant_id AND s.id = i.subscription_id
  JOIN customers cu ON cu.tenant_id = s.tenant_id AND cu.id = s.customer_id
  LEFT JOIN subscription_changes c ON c.tenant_id = i.tenant_id AND c.id = i.change_id
  WHERE i.tenant_id = app_tenant_id() AND i.price_change_id = @price_change_id
) x
WHERE (@state::text = '' OR x.state = @state::text)
  AND (@after_customer::text = '' OR (x.customer_external_id, x.subscription_id) > (@after_customer::text, @after_subscription::uuid))
ORDER BY x.customer_external_id, x.subscription_id
LIMIT @page_size::int;

-- Withdrawing closes the price change: it takes no more subscriptions. Withdrawing one that is withdrawn already does nothing.
-- name: WithdrawPriceChange :execrows
UPDATE price_changes SET state = 'withdrawn', withdrawn_at = now(), withdrawn_by = @withdrawn_by
WHERE tenant_id = app_tenant_id() AND id = @id AND state = 'open';

-- The changes of a price change that are still waiting.
-- name: ListPendingPriceChangeChanges :many
SELECT i.subscription_id, i.change_id
FROM price_change_items i
JOIN subscription_changes c ON c.tenant_id = i.tenant_id AND c.id = i.change_id AND c.state = 'pending'
WHERE i.tenant_id = app_tenant_id() AND i.price_change_id = @price_change_id
ORDER BY i.subscription_id;

-- The kill switch of price changes: one row for the platform (migration 00019). Read by every run, written by the operator.
-- name: GetPriceChangePause :one
SELECT paused, reason, updated_by, updated_at FROM price_change_pause;

-- name: SetPriceChangePause :one
UPDATE price_change_pause SET paused = @paused, reason = @reason, updated_by = @updated_by, updated_at = now() RETURNING paused, reason, updated_by, updated_at;

-- The notice rules of price changes (migration 00029): the notice a run uses when it names none, and the least notice per country ('*' = any other country). Platform
-- data on the system pool; the operator replaces the whole table in one transaction.
-- name: GetPriceChangeNoticeSettings :one
SELECT default_notice_days, updated_by, updated_at FROM price_change_notice_settings;

-- name: SetPriceChangeNoticeSettings :one
UPDATE price_change_notice_settings SET default_notice_days = @default_notice_days, updated_by = @updated_by, updated_at = now() RETURNING default_notice_days, updated_by, updated_at;

-- name: ListPriceChangeNoticeRules :many
SELECT country, min_notice_days, note FROM price_change_notice_rules ORDER BY (country = '*') DESC, country;

-- name: DeletePriceChangeNoticeRules :exec
DELETE FROM price_change_notice_rules;

-- name: InsertPriceChangeNoticeRule :exec
INSERT INTO price_change_notice_rules (country, min_notice_days, note) VALUES (@country, @min_notice_days, @note);
