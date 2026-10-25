# ADR 0017: Fixed fees are billed in advance; plan changes are explicit, previewed and credited

Status: accepted (built in phases 19–22).

## Context

Phase 13 bills the fixed fee with the month's usage, in arrears, and splits a month by time on a plan change. That cannot express a yearly plan, a downgrade at period end, an interval switch, or a
credit that exceeds a charge, and it gives the customer no preview. Paddle and the large billing engines charge recurring fees in advance and prorate with named modes.

## Decision

1. Fixed fees are invoiced at the start of each period (month or year); usage stays in arrears per tenant calendar month.
2. A change has explicit `timing` and `proration`; defaults depend on direction. One function computes the lines for both the preview and the real change.
3. Corrections are credit notes and a per-customer credit balance, never edits of an issued invoice; a card refund is a separate, explicit act.
4. For a provider-owned subscription the provider prorates; Payrudder maps modes onto the provider's and refuses combinations the provider cannot do.
5. Existing subscriptions keep their calendar-month, arrears behaviour; new ones use anniversary anchors and advance fees.

## Consequences

- Billing-svc gains a period-start fee run besides the monthly usage run; both are idempotent per subscription and period.
- Plan version fees are keyed by interval as well as currency.
- Reports (MRR) normalise yearly fees to a monthly amount; revenue recognition schedules are out of scope.
- Two cadences coexist until tenants migrate older subscriptions, which the code must keep supporting.
