# 06 — Paddle: full compatibility, nothing deprecated

## Status (2026-11-02)

**Documentation-closable findings closed; nothing verified against Paddle.** `docs/paddle-compatibility-matrix.md` has the per-finding table ("Status of the findings"), written from the raw markdown pages re-read on 2026-11-02. Summary:

| | |
|---|---|
| Closed | F3 (a duplicate customer: 409 `customer_already_exists` names the owner), F4 (a 429 keeps its code and `Retry-After`, surfaced as `TransientError.RetryAfter` and **honoured by payments-worker's delay queues since the reliability change**: `max(tier delay, Retry-After)`, at most `PAYMENTS_RETRY_CEILING`, default 10 minutes), F6 (invoice link expires in an hour, documented, never stored), F7 (#270), F8 (`payments[]` is newest first: the decline code was the oldest attempt's), F9 (`scheduled_change` carried on `SubscriptionEvent` **and kept in `provider_subscriptions`, returned by the admin API and shown read-only in the console's subscription sheet since the reliability change**); `quotify_*` custom_data keys renamed to `payrudder_*` and still read; a pagination helper for the list calls; a partial refund is a partial adjustment on a one-line-item transaction and refused otherwise (it used to refund the whole transaction whatever the amount); the preview credit sign; unclear points 2, 9, 12, 13, 14. |
| Still UNVERIFIED (docs do not settle it) | F1 (does the non-catalogue price's `custom_data` reach `transaction.completed`), F5 (no documented way to defer an item change to the period end; the refusal stays), whether a charge works on paused/trialing/canceled subscriptions, whether `ts` is regenerated on a webhook retry, the permission `GET /event-types` needs, which countries need a postal code, idempotency headers. |
| Needs a Paddle sandbox account | F1, F2 (how long an immediate charge takes), the `standard` tax category on the account, the simulator (scriptable by API, needs a destination and a public URL), the consent change of 2025-09-16, every end-to-end flow of "Work" 2 and 3. |

Defaults chosen (not agreed): a partial refund across several line items is refused, not split; the retry delay of a rate-limited charge, status query or refund is `max(tier delay, Retry-After)` bounded by `PAYMENTS_RETRY_CEILING` (10 minutes); a payment method change link and the portal link are what Paddle's documentation offers, nothing is invented beyond them. Added: a skipped `TestSandbox` (health check and a paged list; the package comment had claimed one existed) and the conformance suite (plan 08, `docs/payment-provider-contract.md`) run against a fake of Paddle.

## The request
List 1 (8) and list 2 (4): be **exactly compatible with Paddle**, and do **not implement deprecated features**.

## Where we are
`pkg/payments/paddle` (Paddle Billing, API version `Paddle-Version: 1`): hosted checkout (transactions), subscription charge for usage invoices as non-catalogue prices (`PendingError`, outcome by notification), full-refund adjustments, catalogue sync (products/prices), customer/address/business, portal link, pause/resume/cancel, plan change preview/apply, discounts, invoice PDF URL, `Paddle-Signature` webhook verification (`ts;h1`, tolerance 300 s, several `h1`), mode by key prefix (`pdl_live_`, `pdl_sdbx_`) and host. Written from the public docs and the Go SDK types; tested against fixtures. Several behaviours are marked UNVERIFIED in the code.

## Gaps
- **No review against Paddle's current documentation and changelog.** Which endpoints, fields and events the adapter uses were never listed and checked for deprecation or replacement (Paddle Classic is gone; within Billing, fields and events change). The code was never run against a Paddle sandbox (`TestSandbox`, added 2026-11-02, is skipped without `PADDLE_SANDBOX_API_KEY`).
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

## Review addendum (2026-11-01)
- Research steps 1-2 are partly done: `docs/paddle-compatibility-matrix.md` exists (F1-F9 plus a list of unclear points) and #270 fixed the first findings. Remaining: close F1-F9 and the unclear list with fixtures, read the raw documentation (the matrix was read through fetch summaries), and add pagination helpers.
- `quotify_*` custom_data keys remain (`pkg/payments/paddle/api.go`): rename to `payrudder_*` while still reading both. Refund is `type:"full"` only; a partial credit note on a Paddle invoice must be refused or routed to an adjustment (documented, tested).
- Surface `Retry-After` from rate limits. Live behaviour stays UNVERIFIED until a sandbox exists.
- **Done 2026-11-02** (see Status): the first four bullets above except the sandbox-dependent ones, the key rename, pagination, `Retry-After`, partial refunds. Left: the recorded sandbox responses, the sandbox transcript, the nightly sandbox job, the Paddle changelog comparison job (Risks).
