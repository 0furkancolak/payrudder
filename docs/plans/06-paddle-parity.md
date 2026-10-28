# 06 — Paddle: full compatibility, nothing deprecated

## The request
List 1 (8) and list 2 (4): be **exactly compatible with Paddle**, and do **not implement deprecated features**.

## Where we are
`pkg/payments/paddle` (Paddle Billing, API version `Paddle-Version: 1`): hosted checkout (transactions), subscription charge for usage invoices as non-catalogue prices (`PendingError`, outcome by notification), full-refund adjustments, catalogue sync (products/prices), customer/address/business, portal link, pause/resume/cancel, plan change preview/apply, discounts, invoice PDF URL, `Paddle-Signature` webhook verification (`ts;h1`, tolerance 300 s, several `h1`), mode by key prefix (`pdl_live_`, `pdl_sdbx_`) and host. Written from the public docs and the Go SDK types; tested against fixtures. Several behaviours are marked UNVERIFIED in the code.

## Gaps
- **No review against Paddle's current documentation and changelog.** Which endpoints, fields and events the adapter uses were never listed and checked for deprecation or replacement (Paddle Classic is gone; within Billing, fields and events change). The code was never run against a Paddle sandbox (`PADDLE_SANDBOX_API_KEY` test is skipped).
- Unchecked specifics: non-catalogue price shape on `/subscriptions/{id}/charge` and on `/transactions`; `on_payment_failure` semantics; full vs partial refunds (adjustments of type `partial` with `items`); adjustment approval flow; handling of `transaction.updated`, `transaction.billed`, `transaction.past_due`, `subscription.past_due`, `subscription.canceled` with scheduled changes; idempotency (Paddle has no idempotency key for these calls?); pagination (`per_page`, cursor) when listing; rate limits and `Retry-After`; webhook retry window and ordering; simulating events (Paddle's notification simulator).
- Tax: Paddle is merchant of record (it calculates and remits), so our own tax lines on a Paddle-sold subscription may conflict; the interplay with 05 is undecided.
- The adapter returns what it can; capabilities (`OwnsRenewal`, `OwnsDunning`, `SendsOwnReceipts`) drive our behaviour: each claim needs a test against the real flow.

## Research first
1. Read, and cite with dates: Paddle Billing API reference (every endpoint we call), the webhook events list, the "migrate from Classic" and the changelog / deprecations page, the Go/Node SDK release notes, the signature verification doc, the sandbox doc and the notification simulator.
2. Make a **matrix**: each call/event/field our adapter uses → docs section → status (current / deprecated / removed / unclear) → test that proves it. Save it in this plan.
3. Find out what Paddle's own SDK does that we do not (pagination, error type mapping, retries) and decide what to copy.

## Decisions needed
- A **Paddle sandbox account** (API key, webhook secret, a product id) for a real end-to-end run; without it nothing here can be called verified.
- How tax is shown for Paddle-sold subscriptions (their totals are authoritative).

## Work
1. Build the matrix (research step 2) and fix every deprecated or wrong use, with a test per fix.
2. Replace fixtures with **recorded real responses** from the sandbox (scrubbed), kept in `testdata/`; keep the fake only for failure modes.
3. Run the end-to-end flows in the sandbox: checkout → `transaction.completed` → subscription created → usage charge → refund → cancel → pause/resume → plan change; write the transcript in the plan.
4. Webhook hardening: replay, out-of-order, duplicate, unknown event type, oversized body; the notification simulator in CI if it is scriptable.
5. Remove features nobody can justify (anything Paddle deprecates or that has no caller).
6. Docs: what is supported, what is deliberately not.

## Done means
The matrix has no "unclear"; the sandbox transcript is attached; the `PADDLE_SANDBOX_API_KEY` test runs in nightly CI with a secret; the UNVERIFIED markers are gone or explained.

## Not verifiable here
Everything against the live Paddle service until the owner provides sandbox credentials.

## Risks
Paddle changes the API under us; a scheduled job that compares our matrix with the changelog is worth adding.
