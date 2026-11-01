# 13 — Customer block and ban

**Status (2026-11-01): built, with the defaults below (not agreed). Open: "suspend service" (entitlements deny), a block that ends by itself sending an event, notices to the customer, a blocklist and a platform-wide list.**

## The request
List 2 (11): customers can be **banned** and similar.

## Where we are
#252: `customers.blocked_at` / `block_reason`; `POST /v1/customers/{id}/block|unblock` (API, four SDKs); a blocked customer gets **no new subscription** (`plans.customer.blocked`, FAILED_PRECONDITION); existing subscriptions run on; the customer list shows a Blocked badge and offers block/lift and a bulk action.

## Gaps
- **Enforcement is one place** (subscription creation). Not enforced: hosted checkout and payment links for that customer, ad-hoc payment links, credit pack purchases, plan changes and upgrades of existing subscriptions, usage ingestion (a banned customer may still send events and use entitlements), portal access, coupon redemption, new API-created invoices or credit grants.
- "Ban" has several meanings that were not separated: (1) **block new purchases**, (2) **suspend service** (entitlements deny, limits-svc returns not allowed), (3) **end everything** (cancel subscriptions, void open invoices), (4) **blocklist** (email/tax id/card fingerprint so the same person cannot come back under another id), (5) fraud flag with reasons. Only (1) exists.
- No reason categories, no audit trail (who blocked, when, from where) beyond the row, no webhook event (`customer.blocked`) for the seller's systems, no notification to the customer, no expiry ("blocked until"), no appeal.
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
| 11 | Usage ingestion, entitlements and limits | none | **allowed** | the API key is refused or read-only (plan 14) | gateway | gateway auth tests |
| 12 | The seller's own calls for a customer (grant credits, record a payment, create a coupon redemption by hand) | none | allowed: the seller blocked the customer and can still act for them | gateway read-only | – | – |

Not in the table because it is not a path: the `pkg/paymentgate` table test (`TestCheck`) holds every cell of the first two columns.

### Not built, and what it needs

- **Suspend service** (entitlements and limits answer "not allowed", usage ingestion refused): not built. limits-svc builds its map from `plans.entitlements.v1` (one key per tenant, customer and meter), and a meter the plan does not limit has no key, so a per-meter answer cannot deny everything. The clean way is a customer-level record on that compacted topic (`EntitlementState` with an empty meter, or a topic of its own) that `Check` and `CheckEntitlement` read first, published by `BlockCustomer` and `UnblockCustomer` in the same transaction as the block (the outbox exists) and cleared by the end of the block. That touches plans-svc, limits-svc, its Redis mirror and the topic list in `deploy/`; ingest-svc does not block on limits by design (`docs/03-services/limits-svc.md`), so refusing ingestion would be a separate change. Left as the next step of this plan.
- **A sweep for blocks that ended**: it would clear the row and send `customer.unblocked`. Skipped because nothing depends on it (every check reads the end) and the job needs the cells-aware shape of `trialnotices`.
- **Not verified here:** fraud efficacy; the portal and hosted pages in a browser (the Go tests call the handlers and the gateway; no provider checkout was started).
