# Phase 19 — Yearly intervals and fixed fees in advance

## Goal

Subscriptions can be monthly or yearly and the fixed fee is invoiced at the start of each period. See [15-billing-lifecycle.md](../15-billing-lifecycle.md) and [ADR 0017](../adr/0017-advance-fixed-fees-and-change-modes.md).

## Scope

**In:** the `year` interval; plan version fees per interval and currency; `fee_timing` (`advance`, `arrears`) and anniversary anchors on new subscriptions; plans-svc `ListSubscriptionsFeeDue` and `AdvanceSubscriptionFee`;
billing-svc `BillFees` (kind `FEE` invoices, idempotent by `fee_key`); the monthly run leaves the fee off advance-billed subscriptions; MRR counts a yearly fee as a twelfth; gateway/OpenAPI; console (interval on the subscription form, yearly fees on the package form); six languages.

**Out of scope:** plan changes of advance-billed subscriptions (phase 20), credit for a prepaid fee when a subscription ends early or moves provider (phases 20 and 22), Paddle prices per interval (phase 21), existing subscriptions are not converted.

## Decisions made while building

- **Default is `advance`**, except for a provider that renews subscriptions itself (Paddle), which stays `arrears` here because the provider bills the fee. `arrears` stays selectable for monthly subscriptions; the e2e and chaos suites pin it so their invoice totals keep meaning what they meant.
- **Fee periods are tracked in plans-svc** (`subscriptions.fee_due_at`, advanced by compare-and-set) instead of billing-svc scanning every subscription every tick. The query is indexed and returns only what is due.
- **A fee invoice is its own invoice** (kind `FEE`, `fee_key` unique), not a line on the monthly one: it is charged when the period starts and numbered then.
- **Paused periods are skipped**, not deferred: a resumed subscription is not charged for the paused weeks, and the next period is billed normally.
- **A trial moves the anchor** to the trial end, so the first fee is invoiced when the trial ends.
- **`ChangePlan` of an advance-billed subscription is refused** until phase 20 (a clear error names `fee_timing = ARREARS` as the way to the old behaviour).
- **Known gap:** ending an advance-billed subscription early, or migrating it to another provider, does not credit the unused part of the prepaid period yet. Phase 20 (previews, credit balance) and phase 22 (credit notes) close it.

## Tasks

- [x] `pkg/subscription`: yearly anniversary test
- [x] plans-svc: migration 00008, fee per interval, `fee_timing`/`fee_due_at`, the two RPCs, MRR
- [x] billing-svc: migration 00004, `BillFees`, waived fee on the monthly run, integration tests
- [x] Gateway OpenAPI, console, six languages, mocked-gateway e2e
- [x] Docs
- [ ] A yearly subscription run against a provider sandbox
