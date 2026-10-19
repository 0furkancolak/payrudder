# Phase 16 — Routing and provider migration

## Goal

New subscriptions go to the right payment provider by the customer's country and the subscription's currency, and an existing subscription can move from one
provider to another without the customer being billed twice. See [14-billing-platform.md](../14-billing-platform.md).

## Scope

**In:** routing rules (plans-svc, gateway `/v1/routing-rules` with a preview), the provider catalogue (`pkg/payments/providers`), the provider migration (plans-svc state and cutover,
payments-worker orchestration, `/admin/v1/tenants/{id}/provider-migrations`), Paddle subscription cancellation.

**Out of scope:** the console (phase 18), migrating between two providers that both run subscriptions themselves, moving a paused or past-due subscription, carrying a customer's saved card between providers
(the customer subscribes again at the target).

## Decisions made while building

- **Rules are an ordered list, first match wins.** A rule matches a country and a currency, either of which may be empty (any); a rule with neither is the catch-all and must be last. The list is checked when it is
  saved: known providers, a provider that can charge the rule's currency, no rule an earlier one makes unreachable. No match means the tenant's default provider account, as before.
- **The provider is fixed when the subscription is created** (named explicitly, else routed), and a provider that cannot charge the subscription's currency is refused. Editing the rules never moves anyone.
- **A migration never has two providers live at once.** The new subscription starts exactly where the old one ends, in one transaction (a plan-change-style chain, so billing splits the month's fixed fee along it).
  - *To a provider that runs subscriptions (Paddle):* the migration is `started`, a checkout is made, and **the old subscription is untouched** until Paddle's `subscription.created` (carrying the migration id)
    arrives; only then does the cutover happen. A customer who never completes the checkout costs nothing; the migration can be aborted. If no checkout can be made the migration is aborted at once.
  - *To a provider Quotify runs (iyzico):* the cutover is immediate, except from a provider-run subscription, where it is the end of the period that provider was paid for. The old provider is told
    to stop renewing **first** (effective at the end of the paid period); only then is the migration started. A failure in between leaves a subscription that ends without a successor, which an operator retries,
    and never two that both bill.
  - After a cutover to a provider-run subscription, a source subscription that the old provider ran is cancelled there **immediately**: the customer now pays the new provider.
- **Completing is idempotent** (Paddle delivers notifications more than once); completing with another provider id is refused; if the old provider cannot be told, the webhook answers 503 and is retried.
- **The fixed fee is never billed on both sides**: a provider-run subscription is waived on Quotify's invoice (phase 15), and the old subscription's share of the month ends at the cutover.
- **Re-rating** still skips a subscription changed inside the period, so a migration month is not re-rated.

## Tasks

- [x] `pkg/payments/providers`: the catalogue
- [x] plans-svc: routing rules (validation, routing at creation, preview), provider migrations (start, complete, abort), cutover
- [x] Paddle: cancel a subscription, migration id on the checkout and on the subscription event
- [x] payments-worker: migration orchestration, completion from the webhook, release of the old provider
- [x] Gateway routes, OpenAPI; docs
- [ ] A migration run against both providers' sandboxes

## Deliverables

- A Turkish customer in lira goes to iyzico and a customer in dollars to Paddle by rules the tenant edits; a customer moves between them with one fixed fee per period.
