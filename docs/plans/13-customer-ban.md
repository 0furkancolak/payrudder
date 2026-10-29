# 13 — Customer block and ban

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
