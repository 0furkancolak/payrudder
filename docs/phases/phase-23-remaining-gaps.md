# Phase 23 — What phases 19–22 left out

## Goal

Close the gaps phase 22 listed as not built, so that the billing platform of [14-billing-platform.md](../14-billing-platform.md) and [15-billing-lifecycle.md](../15-billing-lifecycle.md) has no stated hole left that Quotify can fill without a provider sandbox.

## Scope

**In (each a PR):**

- **23a — Per-tenant dunning.** `GET/PUT /v1/dunning-settings`: 0 to 6 retry delays, 1–720 hours each (minutes in a sandbox tenant); none set means the service defaults (+1 d, +3 d, +7 d); an empty list gives up on the first decline. The saga reads the tenant's row when it loads, so a change applies to charges already declined. Console card on the pricing tab.
- **23b — Credit for the unused prepaid fee.** `DELETE /v1/subscriptions/{id}?credit_unused=true` ends a subscription billed in advance now and records an adjustment for the rest of the paid period, which billing turns into a credit balance; the response says `credited_minor`. A cutover to another provider does the same for a Quotify-run subscription. A subscription with a coupon is refused for the explicit cancel and skipped at a cutover (what was paid is not the list fee: issue a credit note).
- **23c — Credit notes that refund the card.** `refund_to_card` on a credit note sends a `RefundPayment` command (no saga: the reply is matched by command id) instead of adding to the credit balance. An unanswered refund is sent again with the same idempotency key, three attempts; a refusal fails the note; an operator can retry a failed one (a new key after a definite refusal, the same after silence, so a refund that did go through is never doubled). The note shows `refund_state`.
- **23d — Outbound events.** Webhook endpoints, signing, retries, delivery log, redelivery, test ping, the event catalogue: [16-webhooks.md](../16-webhooks.md). Console **Webhooks** tab.
- **23e — Hosted invoice page.** Unguessable invoice links and a public page, also in [16-webhooks.md](../16-webhooks.md). A share card on the invoice page.

**Out of scope (still not built):**

- **Quantity (seat) changes.** The subscription model has no quantity and the rating has no per-seat price; adding both is a data-model change of its own, not a gap in a flow.
- **Tax calculation** (what is due where), tax-inclusive prices and reverse charge: the rates and their correctness stay the tenant's.
- **Automatic suspension of a webhook endpoint** that fails for days, signed payload versions beyond `v1`, and event replay by time range.
- **Sandbox runs of iyzico and Paddle.** No credentials were available; the points that only a sandbox settles remain marked UNVERIFIED.

## Decisions made while building

- **Events live where the endpoints live.** plans-svc owns the tenant's configuration (rates, coupons, routing), so it owns the endpoints and the delivery. billing-svc does not call out to tenants: it writes events in its transactions and a loop hands them over, so billing never waits on a tenant's server.
- **Subscription events are transactional, invoice events are at-least-once.** Both are idempotent on the event id.
- **The signing secret is sealed, not hashed**, because signing needs it; the key is the one payments-worker already uses (`QUOTIFY_SECRETS_KEY`). Without it tenants cannot create endpoints; nothing else changes.
- **The private-address rule is checked at connect time**, not on the URL, because a name can point anywhere; the dev stack lifts it with a flag the chart cannot set.
- **A refund is not a saga.** The invoice saga is finished when the invoice is paid; reopening it for a partial refund would add states to a table that is checked for completeness. A credit note carries its own small state (requested, refunded, failed) and a resend rule.
- **Credit on early cancel is opt-in.** Ending a subscription without the flag keeps the old behaviour: the customer keeps what they paid for.
- **A hosted link is a capability, not a session.** Anyone with it sees the invoice; that is what "send the customer a link" means. It expires (90 days by default) and can be revoked.

## Exit criteria

Integration tests for each part (plans-svc, billing-svc), unit tests for the signature and the address rule, gateway tests for the hosted page, the mocked-gateway console specs, and the existing end-to-end suites unchanged.
