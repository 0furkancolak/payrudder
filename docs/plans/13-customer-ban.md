# 13 — Customer block and ban

**Status (2026-11-02): built, with the defaults below (not agreed), including "suspend service" (entitlement checks deny) and a sweep that clears a block that ran out and sends `customer.unblocked`. Open: notices to the customer, a blocklist and a platform-wide list; ingestion is deliberately not refused.**

## The request
List 2 (11): customers can be **banned** and similar.

## Where we are
#252: `customers.blocked_at` / `block_reason`; `POST /v1/customers/{id}/block|unblock` (API, four SDKs); a blocked customer gets **no new subscription** (`plans.customer.blocked`, FAILED_PRECONDITION); existing subscriptions run on; the customer list shows a Blocked badge and offers block/lift and a bulk action.

## Gaps
- ~~**Enforcement is one place** (subscription creation).~~ One gate, `pkg/paymentgate`, now decides every path that starts a payment or grants access (the matrix below): hosted checkout, payment links, credit packs, plan changes and price-change scheduling, and entitlement checks when the block suspends the service. **Deliberately not refused:** usage ingestion (a banned customer's events are still counted; the seller's service has to ask before it serves).
- "Ban" has several meanings that were not separated: (1) **block new purchases**, (2) **suspend service** (entitlements deny, limits-svc returns not allowed), (3) **end everything** (cancel subscriptions, void open invoices), (4) **blocklist** (email/tax id/card fingerprint so the same person cannot come back under another id), (5) fraud flag with reasons. Only (1) exists.
- ~~No reason categories, no audit trail, no webhook event (`customer.blocked`), no expiry ("blocked until").~~ Built: categories, `customer_block_events` and `audit_events`, `customer.blocked` / `customer.unblocked`, `blocked_until` with a sweep that clears a block that ran out. **Still not there:** a notice to the customer and an appeal.
- The operator cannot block across tenants (platform-wide fraud list).

## Research first
1. Decide the meanings above with the owner; read how Stripe Radar, Paddle (blocks, fraud) and Chargebee handle it.
2. Trace every path that creates money movement or access for a customer and list where a check belongs.

## Decisions needed
Which of (1)–(5) to support; whether blocking suspends access; policy for existing subscriptions; customer-visible wording.

## Work
1. Spec with the enforcement matrix (path × behaviour).
2. Enforce at every path in the matrix with the same error key; tests per path.
3. Audit table + webhook event + console history; reason categories; optional expiry.
4. Entitlements/limits integration for suspension; blocklist if chosen.
5. Operator-level view for platform fraud (14).

## Done means
Each path in the matrix has a test that a blocked customer is refused; the audit trail and webhook exist; the console shows who/when/why.

## Not verifiable here
Fraud efficacy.

## Risks
A wrong block loses revenue: unblock is instant, audited, and bulk operations are confirmed.

## Done so far (2026-10-29)

Enforcement of "no new purchase" for a blocked customer now covers: subscription creation (#252), plan change (the review-fixes PR) and **every purchase that goes through a payment link or the portal** (an ad-hoc link with the customer set, a credit pack, a portal pack purchase): `CreateLinkInvoice` refuses with `billing.customer.blocked` (FAILED_PRECONDITION), the hosted page says "This payment cannot be made. Please contact the sender." in six languages (not "already used"), a refused start makes no invoice and does not use the link up, and lifting the block lets the same link be used. What was invoiced before the block stays payable ("what exists runs on").

Still open: usage ingestion and entitlements for a blocked customer (suspend service), cancelling everything, a blocklist across customers, reason categories, an audit trail, a `customer.blocked` webhook, "blocked until", operator-wide blocks. Which of those the owner means by "ban" is the decision this plan asked for.

## Review addendum (2026-11-01)
- **Stale:** plan changes are enforced (`subscriptions/command/planchange.go`, #260). **Not enforced:** `SchedulePriceChange` (it uses `changesqry.Price`, bypassing that check) and the hosted `/i/{token}/start|pay` and `/c/{token}/credits/buy|invoices/*/pay` paths until #276 (merged 2026-11-01 for payment links and portal purchases).
- Route is `/v1/customers/{external_id}/block`. A new webhook type touches `plans-svc/internal/webhooks/model/model.go`, `docs/16-webhooks.md`, `api/openapi/v1.yaml`, `web/admin/src/features/webhooks/api.ts` and the four SDK webhook files.
- **One gate:** implement a single `paymentGate(tenant, customer)` used by plans 13, 14 and 21; error key `plans.customer.blocked` / `billing.customer.blocked` as already registered.
- **Defaults (not agreed):** "ban" = block new purchases (done) + optional suspend (entitlements deny) + reason category + optional `blocked_until`; blocklist and platform-wide fraud list are out of scope for now.

## Done (2026-11-01, branch `feat/block-and-tenant-status`)

Defaults the implementer chose, because the owner said not to ask (**default, not agreed**; the owner may overturn any of them):

1. **"Ban" = block new purchases** (done in #252, #260, #276) **+ a reason category + an optional end** (`blocked_until`). Categories: `fraud`, `chargeback`, `abuse`, `request`, `other` (empty allowed). The end must be in the future; every check reads it, so a block **stops holding at that time with no job** (the row keeps saying "blocked" until somebody lifts it; the console and the API say it ended). A block after one that ran out starts afresh.
2. **What was invoiced before a block stays payable** (the seller is owed it), **except after a `fraud` or `chargeback` block**, which also refuses paying an invoice that is open. This is the one rule in the table below that depends on the category.
3. **One gate.** `pkg/paymentgate.Check(op, tenantStatus, block, now)` decides for every path; callers only turn its verdict into their own error key (`plans.customer.blocked`, `billing.customer.blocked`, and `plans.tenant.on_hold` / `billing.tenant.on_hold` for a tenant the operator has taken out of service, plan 14). Plan 21's acceptance gate and plan 04's pages are to use it too.
4. **History** (who, when, why): `customer_block_events` (plans-svc, tenant-scoped with its `migrations/tenant` twin), append-only by privilege (`plans_app` may only `INSERT` and `SELECT`), the pattern of `go_live_audit`; it moves to `audit_events` of plan 22 (G4) when that exists. `GET /v1/customers/{external_id}/block-events` returns the latest 100; the customer sheet shows them. The actor is the signed-in person's address, `key:<api key id>` for an API key, or `operator`; the gateway sets it, a body cannot.
5. **Webhooks** `customer.blocked` (`customer_id`, `customer_external_id`, `category`, `reason`, `blocked_until`) and `customer.unblocked`, through the whole checklist (model, four SDK enums and data types, console list, `docs/16-webhooks.md`, the OpenAPI schemas). Blocking a customer who is blocked sends nothing. **The end of a block by itself sends no event** (no job exists to send it); lifting a block that had ended does.
6. **Out of scope, as the review addendum said:** the blocklist across customers, the platform-wide fraud list, notices to the customer, appeals, cancelling everything.

### Path by gate

Every path that starts a payment or grants access, the gate it asks, and the test that holds the row. "Held" is a tenant the operator has suspended, frozen or banned.

| # | Path | Gate | Customer blocked | Tenant held | Where | Test |
|---|---|---|---|---|---|---|
| 1 | Create a subscription | purchase | refused, `plans.customer.blocked` | refused, `plans.tenant.on_hold` | plans-svc `subscriptions/command/create.go` | `CustomerBlock`, `TenantStatus` (plans-svc itest) |
| 2 | Change a subscription's plan | purchase | refused, `plans.customer.blocked` | refused, `plans.tenant.on_hold` | `subscriptions/command/planchange.go` | `CustomerBlock` |
| 3 | Scheduled price change (`SchedulePriceChange`) | customer only | skipped, `customer_blocked` in `skipped` (also in a dry run) | refused at the gateway (read-only) | `changes/command/pricechange.go` | `CustomerBlockDetails` |
| 4 | First use of an ad-hoc or credit-pack link (`POST /i/{token}/start`) | purchase | refused, `billing.customer.blocked`; no invoice, the link is not used up | refused, `billing.tenant.on_hold` | billing-svc `hosted/command/linkinvoice.go` | `ABlockedCustomerCannotStartAPurchase`, `TheGateRefusesPayments`; gateway `TestGateRefusalsSayThePaymentCannotBeMade` |
| 5 | Buy a credit pack in the portal (`POST /c/{token}/credits/buy`) | purchase | refused (and the portal hides Buy) | refused | `hosted/command/payment.go` (`StartPortalPurchase` makes a link, then 4) | `TheGateRefusesPayments` |
| 6 | Pay an open invoice (`POST /i/{token}/pay`, `POST /c/{token}/invoices/{id}/pay`) | pay invoice | **allowed**, except `fraud` and `chargeback` (`billing.customer.blocked`) | refused, `billing.tenant.on_hold` | `hosted/command/payment.go` `RequestPayment` | `TheGateRefusesPayments`; gateway test above |
| 7 | Make a payment link (`POST /v1/payment-links`) | purchase (ad-hoc, credit pack), pay invoice (link to an invoice) | refused when it names a blocked customer | refused | `hosted/command/command.go` `CreateLink` | `TheGateRefusesPayments` |
| 8 | The Pay and Buy buttons of the hosted page and portal | the same gates | hidden | hidden | `hosted/handler.go` | `TheGateRefusesPayments` |
| 9 | Dunning retry of a declined charge | tenant only | runs on (the subscription exists) | put off one hour, not given up | billing-svc `sagas/command/sweep.go` | `DunningWaitsForATenantOnHold` |
| 10 | The scheduler raising invoices | tenant only | runs on | skipped | `rating/inputs.Billable` | `TestBillableFollowsTheTenantStatus` |
| 11 | Usage ingestion | none | **allowed** (default, not agreed: the events are facts the seller is owed for; see "Suspend service" below) | the API key is refused or read-only (plan 14) | gateway | gateway auth tests |
| 11b | Entitlement checks (`CheckEntitlement`, `GetLimitStatus`) | none | **allowed**, or **not allowed on every meter** when the block was made with `suspend_service` | the API key is refused or read-only (plan 14) | limits-svc `status/query` | `TestSuspension`, limits `SuspensionComesFromTheTopic`, `ASuspendedCustomerIsAllowedNothing` |
| 12 | The seller's own calls for a customer (grant credits, record a payment, create a coupon redemption by hand) | none | allowed: the seller blocked the customer and can still act for them | gateway read-only | – | billing `TheGateRefusesPayments` (grant credits to a customer blocked for fraud), `OfflinePaymentsSettleAnInvoice` (the customer is blocked for fraud while a payment is recorded), plans-svc `CustomerBlockSweep` (a coupon applied by hand to a customer blocked for fraud with the service suspended) |

Not in the table because it is not a path: the `pkg/paymentgate` table test (`TestCheck`) holds every cell of the first two columns.

### Not built, and what it needs

- **Refusing usage ingestion for a suspended customer**: not built, on purpose (see below).
- ~~**A sweep for blocks that ended**~~: built, see "Leftovers" below.
- **Not verified here:** fraud efficacy; the portal and hosted pages in a browser (the Go tests call the handlers and the gateway; no provider checkout was started).

## Done (2026-11-02, branch `feat/suspend-alerts-health-ui`): suspend service

Defaults the implementer chose (**default, not agreed**):

1. **Suspending is an option of the block**, not a second state: `POST /v1/customers/{id}/block` takes `suspend_service` (bool, default false). A block already in force is kept as it is (including its suspension): to change it, lift it and block again, which writes both to the history. The customer carries `service_suspended`, and each history row `suspend_service`; both are read together with `block_time` / `blocked_until` (a block that ended by itself no longer suspends anything).
2. **Entitlement checks deny.** `LimitStatus.suspended` (REST `suspended` on `EntitlementStatus`) is true on every meter of the customer and `allowed` is false whatever the limit, the mode (soft and notify too) and the usage, also on a meter the plan does not limit. A meter that does not exist is still `NOT_FOUND`. The seller's service has to ask before it serves; Payrudder does not stop the seller's own service.
3. **Ingestion still accepts and counts** (the question of the task: *accepted, flagged, or refused?* Default: **accepted, not flagged**). A usage event is a fact that already happened and the seller is owed for it (invoicing is unchanged); refusing it would leave a hole in the period when the suspension ends, and flagging it would make ingest-svc read the entitlements topic, which it deliberately does not. The `used` figure of a suspended customer keeps growing. If the owner wants ingestion refused, it is a gateway check against the block (the gateway already resolves the customer) and an error key; nothing here prevents it.
4. **The record.** `plans.entitlements.v1`, key `{tenant}/{customer}/` (an empty meter), an `EntitlementState` with `suspended = true`, `valid_from` the time of the block and `valid_until` the block's end when it has one. `entitlements/command.Suspend` and `Unsuspend` write it to the outbox **in the transaction of the block or the lift** (a tombstone for the lift). A block that ran out and is replaced by one that does not suspend writes the tombstone too. limits-svc reads it into `Store.suspended` and the Redis mirror `lim:ent:{tenant}:{customer}:`; because it carries `valid_until`, the suspension ends with the block with no job (as every other check of a block does). The customer's limits are never touched, so lifting the block restores exactly what was there.
5. **Webhooks and history.** `customer.blocked` carries `suspend_service`; the history row records it. The console block form has a switch, the sheet's banner says "Service suspended" while it holds, the history lists it. Six locales; no new error key (the answer is data, not an error).
6. **Tests.** limits-svc: `TestSuspension` (unit: from, until, lift by a record or a tombstone, not an entitlement); integration `TestLimits/SuspensionComesFromTheTopic` (a real Redpanda and Redis: the record on the key of an empty meter, a meter with a limit and one without, usage still counted, the tombstone) and `TestQuotas/ASuspendedCustomerIsAllowedNothing` (soft limit, `GetLimitStatus`, another customer untouched, an ended suspension). plans-svc: `CustomerBlockDetails` (the record published with its `valid_until`, no record for a plain block, a tombstone on lift and on a plain block after one that suspended, nothing on a repeated block, the webhook data). Gateway: the request field and the response field. The limits test fails without the change (checked once by forcing `Suspended` to false).
7. **Not verified here:** the portal and the console in a real browser (the mock e2e ran), an upgrade with the old limits-svc running: an older limits-svc reads the record, finds an empty meter and logs "skipping invalid entitlement" without failing, so roll limits-svc out before the first suspension.

### The first charge of an invoice finalized before a hold (plan 14), not built

`PdfRendered --Continue--> PaymentRequested` (effect `charge`) cannot hold today without a new place to wait. Holding it in the table needs a state or a guard (`PAYMENT_HELD` with a retry date, or the existing `PAYMENT_DECLINED` + dunning, which would count the held charge as a dunning attempt and show a decline that never happened), the engine asking `Hold` when it fires the charge, the retry sweep to learn the state, the diagram in `billing-svc.md` and the exhaustive table tests, the console's state labels, and a decision about an invoice held for long (void? dunning?). That is a change to the payment saga, which this plan keeps out of the same PR as the customer switch; **default (not agreed):** a tenant frozen between a finalize and its charge is charged once, as plan 14 already says.

## Leftovers built (2026-11-02, branch `feat/product-leftovers-2`)

Defaults the implementer chose (**default, not agreed**):

1. **A sweep clears a block that ran out.** `customers/command.Sweeper` (plans-svc, every 5 minutes, one batch of 200 per list, again at once while a batch fills) lists the customers whose `blocked_until` has passed on the system pool, and for each, in the tenant's own transaction (cells-aware, as the trial reminder is), locks the row, reads it again and lifts it with the same function as `POST /v1/customers/{id}/unblock`: the row is cleared, the history gets an `unblocked` row with the actor `system`, a suspension that block published ends (the tombstone), the audit entry is written and `customer.unblocked` is queued (same data as a lift by hand, no new field). A block that was lifted, replaced or extended since the list was read is left alone; a second pass, or a second replica, finds nothing. Nothing depended on it before (every check reads the end), so it is only the event and a row that says what is true. `docs/16-webhooks.md` says the event can come minutes after the end.
2. **Row 12 has tests.** The seller's own calls are not gated, and now a test holds that: billing `TheGateRefusesPayments` (credits granted to a customer blocked for fraud), `OfflinePaymentsSettleAnInvoice` (a payment recorded while the customer is blocked for fraud) and plans-svc `CustomerBlockSweep` (a coupon applied by hand). They are pin tests: the cells were already allowed by absence of a check, and a future gate would break them.
3. **The first charge of an invoice finalized before a freeze stays as the plan says** (documented above, **default, not agreed**): it needs a hold outcome in the payment saga's transition table, which is not small. Not built.

Tests: plans-svc integration `TestPlansSvc/CustomerBlockSweep` (ended block cleared, running and unending blocks left, history by `system`, one tombstone, one webhook, second pass nothing, coupon by hand), `TestCellsJobs/{pooled,cells,mixed}/BlockSweep` (every serving tenant visited once, a moving tenant left until it is released); billing `TestBilling/TheGateRefusesPayments`, `OfflinePaymentsSettleAnInvoice`. **Not verified here:** the sweep against a production-size customer table; its list reads a partial index (migration 00018, tenant 00014) that holds only blocked customers with an end.
