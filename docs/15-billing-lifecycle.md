# Billing lifecycle: yearly plans, plan changes, Paddle catalogue, billing documents

Status: approved; phases 19–22 are built; section 4 lists what phase 22 left out. (Quantity changes, listed in section 2 below, were dropped from phase 20: the data model has no quantity.) Builds on [14-billing-platform.md](14-billing-platform.md) and [ADR 0016](adr/0016-provider-capabilities-and-routing.md); decisions in [ADR 0017](adr/0017-advance-fixed-fees-and-change-modes.md).

## Why

Phases 13–18 bill like a metering product: calendar months, in arrears, one plan change mode (split the month by time), and yearly refused. A SaaS billing engine at the level of Stripe Billing,
Chargebee or Paddle is expected to sell monthly and yearly plans, charge the fixed fee **in advance**, let a customer upgrade now and downgrade later, switch monthly to yearly and back with a correct credit,
issue credit notes, and use a provider's whole API (catalogue, customers, discounts, payment-method updates), not only charging.

## Goals

- Yearly and monthly intervals per subscription; fixed fee in advance, usage in arrears.
- Upgrade, downgrade, interval switch (monthly↔yearly), quantity change, each with an explicit **timing** and **proration** choice, a **preview** before commit, and a scheduled-change record.
- Credit notes and a customer credit balance, so a proration never needs a refund to be correct.
- Paddle: Quotify creates and keeps in sync everything Paddle needs (products, prices per interval and currency, customers, addresses, discounts) and drives every subscription operation Paddle offers.
- Same user-facing operations for iyzico, where Quotify does the work itself (adapters declare what they can do; Quotify fills the rest).
- Invoices a finance team accepts: sequential numbers, line items with period, proration lines, tax lines, credit notes, PDF.

## Non-goals

- Becoming a merchant of record; tax calculation for iyzico subscriptions (the tenant's tax setup is applied as given). Paddle's tax stays Paddle's.
- Multi-year contracts, ramp deals, seat-based tiered/volume pricing (quantity is a plain multiplier here), usage commitments and overage bundles.
- Currency conversion.

## Anti-goals

- A change that charges twice, or leaves a customer on two plans at once. A preview that disagrees with what is then billed.
- Quotify and Paddle both prorating the same change. For a provider-owned subscription the provider prorates and Quotify mirrors.
- Rewriting history: an issued invoice is never edited; a correction is a credit note plus a new invoice.

## 1. Intervals and cadence (phase 19)

- A subscription has `interval` (`month`, `year`) and an **anchor**. New subscriptions anchor on their start (billing anniversary); existing calendar-month subscriptions keep their month-start anchor. `pkg/subscription.PeriodEnd` already supports both intervals and month-end clamping.
- **Fixed fee: in advance**, at the start of each period, as its own invoice line for the period `[start, end)`. **Usage: in arrears**, one invoice per tenant calendar month (unchanged), also for yearly subscriptions, so usage is never held for a year.
- Trial: no fixed fee until it ends; the first fee invoice is dated at the trial end.
- Quotify-owned renewal is driven by a scheduler in billing-svc (period-start fee runs, idempotent per subscription and period). Provider-owned (Paddle) renewals arrive as `transaction.completed` and are mirrored.
- Plans carry a fee per interval and currency (`plan_version_fees(interval, currency)`); a yearly price is its own number, not 12× monthly. A plan version that lacks a price for an interval cannot be sold at it.

## 2. Plan changes (phase 20)

One operation, `ChangeSubscription`, with explicit inputs: new plan version, new interval, quantity, `timing`, `proration`.

| Change | Default timing | Default proration |
|---|---|---|
| Upgrade (higher fee per day) | immediately | prorated: credit the unused part of the old fee, charge the new fee for the rest |
| Downgrade (lower) | at period end | none; scheduled change record |
| Monthly → yearly | immediately | credit the unused monthly fee, charge the year; a new yearly period starts now |
| Yearly → monthly | at period end | none; the paid year runs out, monthly starts after (an immediate switch credits the unused year to the credit balance) |
| Quantity change | immediately | prorated |

- `timing`: `immediately` | `period_end`. `proration`: `prorate` | `none` | `full` (new fee in full, old unused credited in full). Tenant sets the defaults per direction; the request overrides.
- **Preview** (`PreviewChange`) returns the exact lines, credits, amount due now and next renewal amount, and the same code path computes the real change (one function, two outputs).
- Immediate upgrades charge the difference at once (an invoice with proration lines). A credit that exceeds the charge goes to the **credit balance**, applied to the next invoice; refunds to the card are an explicit separate act.
- Scheduled changes live in `subscription_changes` (effective at, applied or cancelled), are visible in the console and API, and can be cancelled before they apply. Applying is idempotent and runs in the period-start run.
- Proration is by time to the second over the period, rounded half-even once per line (the existing `subscription.Portion`), in the subscription's currency.
- Paddle-owned subscriptions: map to `PATCH /subscriptions/{id}` with the matching `proration_billing_mode` and `scheduled_change` handling; a preview calls Paddle's preview endpoint; Quotify mirrors the result from the webhook. Where Paddle cannot do a combination (it prorates by its own rules), Quotify refuses it with the reason instead of approximating.

## 3. Paddle through Payrudder (phase 21)

A provider-neutral `Catalogue` capability (optional interface, like `CheckoutProvider`) so the console and API work the same for any provider that has one:

- **Products and prices**: publishing a plan version creates (or updates) a Paddle product and one price per interval and currency; the Paddle ids are stored on the plan version fee rows. Prices are immutable in Paddle, so a changed fee creates a new price and archives the old one.
- **Customers, addresses, businesses**: created on first checkout or on demand; country, tax identifier and business name flow from the Quotify customer.
- **Subscriptions**: create via checkout (hosted), get, update items/quantity, pause, resume, cancel (now or period end), preview, one-time charge (exists), update-payment-method link, scheduled change read and removal.
- **Discounts**: Quotify coupons map to Paddle discounts (percentage / flat, duration, usage limit, expiry) and attach to a checkout or subscription.
- **Transactions, adjustments, invoices**: list, refund (exists, extended to partial and item refunds), credit adjustment, invoice PDF link.
- **Customer portal**: a portal session link for the end customer (payment methods, invoices).
- **Notification settings** and webhook event coverage extended (subscription.updated/paused/resumed/canceled/past_due, transaction.billed/paid, adjustment.*, customer.*, discount.*); each event verified by signature as today.
- Every call goes through the same sealed account, a typed error model, retry on 429/5xx with backoff, and a recorded-fixture test. The sandbox test stays skipped until credentials exist, and what no sandbox confirmed stays marked UNVERIFIED.
- iyzico keeps Quotify as engine: the same API operations are served by Quotify itself (coupons applied by Quotify, payment-method update via iyzico card storage, no portal).

## 4. Billing documents (phase 22)

- **Invoice numbering**: gapless per tenant and year (`INV-2026-000123`), assigned at finalisation, not at draft.
- **Line items** carry a kind (`fixed_fee`, `usage`, `proration_credit`, `proration_charge`, `discount`, `tax`), the service period, quantity and unit amount.
- **Credit notes**: issued against a paid or open invoice (full or partial, with reason), numbered `CN-…`, reduce the amount owed or add to the credit balance, or trigger a provider refund when asked. The ledger links refund ↔ credit note ↔ invoice.
- **Credit balance** per customer and currency, applied automatically at finalisation, shown on reports.
- **Coupons**: percentage or amount, once / repeating N periods / forever, redeem limits and expiry, applied as a discount line; for Paddle-owned subscriptions mapped to a Paddle discount.
- **Tax lines**: stored as given (tenant-supplied rate by country, or the provider's amount for Paddle); never calculated by Quotify beyond applying a configured rate.
- **PDF and hosted invoice page**: rendered from the invoice by the existing PDF worker; link in the API and console.
- **Outbound events** for the tenant's own systems: `subscription.*`, `invoice.*`, `payment.*`, `credit_note.*` through the existing webhook delivery.
- **Dunning settings** per tenant (retry schedule, grace days, final action cancel/pause) for Quotify-owned subscriptions.
- **Console**: change dialog with the live preview, scheduled-change banner, credit notes and coupons pages, invoice detail with lines and PDF, catalogue sync status per plan, all from the shadcn-vue kit and in six languages.

## Delivery

Four phases in order; each ends with green CI and a merge. Provider behaviour that only a sandbox can settle is marked UNVERIFIED and listed in the phase file, as before.

| Phase | Result |
|---|---|
| 19 Yearly and advance fees | A yearly and a monthly subscription are invoiced correctly: fee in advance, usage in arrears, trial, pause, cancel |
| 20 Plan changes | Upgrade, downgrade, interval switches, quantity; preview equals billed amount; scheduled changes; credit balance |
| 21 Paddle catalogue and subscription API | Publishing a plan creates Paddle products/prices; every subscription operation works through Quotify; fixtures cover each call |
| 22 Billing documents | Numbered invoices with typed lines, credit notes, coupons, PDF, outbound events, dunning settings, console |

## Open points for the owner

1. **Anniversary vs calendar anchor** for new subscriptions (recommended: anniversary; existing calendar subscriptions unchanged).
2. **Fixed fee in advance** replaces arrears for new subscriptions (recommended; required for yearly). Existing subscriptions keep arrears until the tenant migrates them.
3. Default downgrade timing at period end and defaults above are acceptable, or should downgrades also apply at once.
4. Credit balance may be refunded to the card only on explicit request, never automatically.
5. Paddle features outside this list (e.g. Paddle's own reports, Paddle Retain) are left out.
