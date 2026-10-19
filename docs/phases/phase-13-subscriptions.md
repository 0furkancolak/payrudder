# Phase 13 — Customers, packages, subscriptions

## Goal

A subscription has a lifecycle (trial, active, paused, cancelled), is sold in a currency, and its fixed fee follows the plan it is on.
Customers carry a country and a billing currency. Plan versions can have a fixed fee and usage prices per currency. See
[14-billing-platform.md](../14-billing-platform.md) for the whole picture.

## Scope

**In:** `pkg/subscription` (states, events, period math, fee portions), `plans_db` migration 00004, the plans-svc API
(trial, pause, resume, cancel at period end, change plan, get), per-currency fees and usage prices, and billing-svc invoicing that
follows them (invoice currency, trial/pause waiver, fixed fee split on a plan change). Gateway routes and OpenAPI.

**Out of scope:** payment providers (phases 14–16), the console (phase 18), yearly subscriptions, re-rating of subscriptions in
another currency or changed inside the period, a proration that charges at the moment of the change.

## Decisions made while building

- **Billing periods stay the calendar months of the tenant.** A subscription's `billing_anchor` is the first instant of the month it
  started in (tenant time zone), so its periods are exactly the periods billing-svc closes. Anniversary billing is not built.
- **Fixed fee is invoiced with the period, as before.** A plan change inside a month splits that month's fee between the two plans in
  proportion to the time on each (`subscription.Portion`, half-even, once per segment). There is no stored proration amount to keep in sync.
  `ChangePlan` returns the two shares for display; billing recomputes them from the chain (`changed_from_id`).
- **A subscription still pays its whole first month**, as it always did. Only a plan change splits a month.
- **Trial:** the fixed fee is waived for a period that ends before the trial does. A trial ending inside a period leaves that period's fee whole.
- **Pause:** the fixed fee is waived for a period that began while the subscription was paused. Pausing mid-period still bills that period.
  Usage is billed in every case.
- **States run out by themselves.** A trial that is over reads as active, a subscription whose range has ended reads as cancelled; the stored
  state is updated when something else happens to the subscription. `past_due` is set by the payment flow (phase 14).
- **A currency needs a fee, and metered plans need usage prices in it.** A plan version without a fee in a currency cannot be sold in it, and one
  with metered prices needs a price set in it, or `CreateSubscription` is refused.
- **Yearly is refused** (`Unimplemented`) until fixed fees can be invoiced once a year.
- **Re-rating** skips subscriptions in another currency than the tenant's and subscriptions changed inside the period, and logs it.

## Tasks

- [x] `pkg/subscription`: state machine, period arithmetic, `Portion`
- [x] `plans_db` 00004: customer country and billing currency, `plan_version_fees`, `plan_currency_prices`, subscription state, interval, currency, trial, pause, anchor, chain
- [x] plans-svc API and gateway routes: get, pause, resume, cancel at period end (and withdraw), change plan
- [x] billing-svc: invoice currency, trial and pause waiver, fee split on a plan change
- [x] Tests: unit (state machine, periods, portions, fee lines, chain), plans-svc and billing-svc integration
- [x] Docs

## Deliverables

- A customer on a trial, a paused customer and a customer who changed plan are each invoiced correctly; a EUR customer of a USD tenant is invoiced in EUR.
