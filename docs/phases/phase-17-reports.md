# Phase 17 — Reports

## Goal

A tenant sees what it billed, collected, refunded and is still owed, what its subscriptions are worth each month, which payments are stuck, and whether the payment ledger agrees with its invoices, split by
payment provider, customer country and currency. See [14-billing-platform.md](../14-billing-platform.md).

## Scope

**In:** `GET /v1/reports/revenue`, `/v1/reports/dunning`, `/v1/reports/subscriptions` (tenant API) and `GET /admin/v1/tenants/{id}/reconciliation` (operator), with provider and country as columns on invoices.

**Out of scope:** the console pages (phase 18), a reporting currency with exchange rates (a currency is never added to another), ClickHouse, export files, reports over time ranges longer than the data kept.

## Decisions made while building

- **Reports read the services' own databases, not ClickHouse.** The design sent ledger events to analytics-svc; the figures needed here are sums over rows that already carry period, provider, country and currency, so they are queries, and
  there is no second copy to fall out of step. A ClickHouse read model can replace them when volumes call for it.
- **Revenue comes from invoices (billing-svc).** *Billed* is finalized invoices that are not void; *collected* paid; *outstanding* open; *uncollectible*; *void*; *refunded* the invoices whose saga passed through `REFUNDED`,
  whatever their status now (voiding a paid invoice refunds it first). Invoices gained `provider` and `country` columns, set when the draft is made.
- **Subscriptions and MRR come from plans-svc.** A subscription counts at the month's last instant if its range then contained it; MRR is the fixed fee, in the subscription's currency, of the active and past due ones (a provider-run subscription
  counts: its fee is recurring revenue even though Paddle, not Payrudder, invoices it). *New* excludes plan changes and provider migrations (they have a predecessor); *churned* excludes subscriptions that have a successor.
  What a subscription was in a past month is only as good as what is stored: a resumed pause or a cleared past-due is not remembered, so for a past month the paused and past due counts are those still in that state.
- **Reconciliation compares the invoices billing calls paid with the ledger's successful charges** and reports `paid_without_charge`, `amount_mismatch`, `duplicate_charge` (two distinct successful charges for one invoice),
  `charge_without_paid` and `conflicting_outcomes` (a success and a failure under one provider reference), with complete counts and at most 200 listed. It needs billing-svc (`PAYRUDDER_BILLING_ADDR`).
- **Zero-total invoices are not charged**, so reconciliation does not expect a charge for them.

## Tasks

- [x] billing-svc: invoice `provider` and `country`, revenue report, dunning report
- [x] plans-svc: subscription report (MRR, new, churned)
- [x] payments-worker: reconciliation
- [x] Gateway routes, OpenAPI, compose; docs

## Deliverables

- One page of figures per provider, country and currency, and a list of every place the ledger and the invoices disagree.
