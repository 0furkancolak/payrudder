# Phase 20 — Plan changes: upgrade, downgrade, interval switch

## Goal

A customer can move between packages and between monthly and yearly with a correct, previewed, auditable result. See [15-billing-lifecycle.md](../15-billing-lifecycle.md) and [ADR 0017](../adr/0017-advance-fixed-fees-and-change-modes.md).

## Scope

**In:** `pkg/subscription.QuoteChange` (one calculation for preview and change); `ChangeSubscription`, `PreviewSubscriptionChange`, `CancelScheduledChange`, `ApplyScheduledChanges` and the adjustment RPCs in plans-svc;
`subscription_changes` and `subscription_adjustments`; billing-svc turning adjustments into an invoice or a credit balance and applying due changes in its tick; credit balances per currency; gateway routes
(`/change-preview`, `/change`, `/scheduled-change`); OpenAPI; a console panel with a preview; six languages.

**Out of scope:** changes of Paddle-owned subscriptions (phase 21 maps them to Paddle's modes), card refunds of a credit balance and credit notes (phase 22), arrears subscriptions (they keep `ChangePlan`),
**quantity changes** (the data model has no quantity; the design's quantity row is dropped here and comes back only with a seat or quantity model), credit for the unused prepaid period on cancellation or provider migration (phase 22).

## Decisions made while building

- **One calculation.** `QuoteChange` returns timing, proration, credit, charge, net, the new anchor and the next fee period; the preview returns it and the change performs it. The integration test compares the two.
- **Defaults by direction.** Within an interval a cheaper plan waits for the end of the period (the customer keeps what was paid for); monthly-to-yearly is immediate because the year is a commitment made now and is usually cheaper per month, so comparing it with the monthly price would wrongly call it a downgrade; yearly-to-monthly waits for the end of the paid year.
- **Money moves through adjustments, not inside the change.** plans-svc stays the owner of the domain and never creates invoices; billing-svc pulls adjustments on its tick (idempotent by key), exactly like fees. The cost is a delay of one tick between a change and its invoice.
- **A change needs the current period's fee to be invoiced.** Otherwise there is nothing prepaid to credit, so the change answers `ABORTED` (retry in a moment).
- **Credit is a balance, not a refund.** A downgrade now (or yearly-to-monthly now) adds the difference to the customer's credit balance, which pays their next invoice in the same currency.
- **Trial: no money.** The plan changes at once; the first fee, at the trial end, is the new plan's.
- **Scheduled changes lapse** when the subscription is paused, past due, ended or ending at the time they would apply.

## Tasks

- [x] `pkg/subscription`: `QuoteChange` and tests for every mode and direction
- [x] plans-svc: migration 00009, RPCs, scheduled changes, adjustments, integration tests
- [x] billing-svc: migration 00005, adjustments to invoice or credit, apply scheduled changes, credits per currency, integration tests
- [x] Gateway routes, OpenAPI, console change panel with preview, six languages, mocked e2e
- [ ] Paddle mapping of the same modes (phase 21)
- [ ] A change run against a provider sandbox
