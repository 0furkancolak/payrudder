# Phase 22 — Billing documents

## Goal

Invoices, credit notes, coupons and tax that a finance team accepts. See [15-billing-lifecycle.md](../15-billing-lifecycle.md) and [ADR 0017](../adr/0017-advance-fixed-fees-and-change-modes.md).

## Scope

**In:**

- **Credit notes** (billing-svc): numbered like invoices and gap-free per tenant and year (`CN-2026-000007`, `SBX-CN-…` for sandbox), issued against a *paid* invoice, never more than the invoice in total; the amount goes to the customer's credit balance in the invoice's currency and pays their next invoice.
  `CreateCreditNote` (operator) and `GetCreditNote`/`ListCreditNotes` (tenant); the revenue report has a `credited_minor` column.
- **Typed lines with service periods:** invoice lines can be `DISCOUNT` or `TAX`, and carry `service_from`/`service_to` (the month for usage, the exact segment for a fixed fee, the fee period for an advance fee).
- **Coupons** (plans-svc): percentage (basis points) or amount in a currency; once, for N fees, or forever; redemption limit and expiry; one per subscription; carried over a plan change with the fees it has used; a `DISCOUNT` line on the advance fee invoice.
- **Tax rates** (plans-svc): per customer country, with a rate for every other country; added as a `TAX` line, exclusive of the price, on the invoices Quotify raises (monthly usage, advance fees, what a plan change moved). The subtotal is before tax.
- Gateway routes and OpenAPI for all of these; console: a **Coupons & tax** tab, a credit-note card on the invoice page and a list on Payments, a coupon field and buttons on subscriptions, the service period on invoice lines, a "Credited" report column; six languages.

**Out of scope (not done):**

- **Outbound events** (`credit_note.created`, `subscription.changed`…) for the tenant's own systems, and a **hosted invoice page**: neither exists yet.
- **Per-tenant dunning settings.** The retry delays and the final action (uncollectible) are still the deployment's (`QUOTIFY_DUNNING_*`), as before.
- **A credit note that refunds the card.** A credit note becomes a credit balance; refunding a card stays "void the invoice" (full, through the provider).
- Tax calculation (what is due where), inclusive prices, reverse charge, and tax on re-rating corrections. The rates and their correctness are the tenant's.
- Coupons on subscriptions a provider runs (Paddle's own discounts are created through the provider API, phase 21), on usage charges, and on a plan change's proration charge.

## Decisions made while building

- **A credit note only corrects a paid invoice.** An open invoice is still being collected (voiding it is the correction); changing a total under a running payment saga would need a second protocol. The sum of an invoice's notes is checked under the invoice's row lock.
- **A credit balance has a currency** (migration `credit_balances.currency`) and is applied only to invoices in it; before, any credit could land on any invoice. It has exactly one source: a re-rating, a plan-change adjustment or a credit note.
- **Coupons are applied where the fee is invoiced.** plans-svc computes the discount when it lists the fee that is due and counts the period when the fee period is advanced (compare-and-set, so once), so a crash between the invoice and the count repeats harmlessly and a coupon can never be used up twice.
- **Tax base:** everything except a credit balance and tax itself. A discount lowers the base; a credit does not (it pays the invoice, it does not change what was sold). Rounded half-even once per invoice.
- **A provider taxes what it charges for.** An invoice for a subscription a provider runs gets no `TAX` line from Quotify (Paddle's merchant-of-record tax is Paddle's).
- **`FEE` invoices are real invoices:** gap-free numbers, the same payment saga, PDF with the new lines (the PDF's subtotal excludes tax).

## Tasks

- [x] billing-svc: migrations 00006 (credit notes) and 00007 (line kinds, service periods); credit notes; discount and tax lines; credits per currency
- [x] plans-svc: migrations 00010 (coupons) and 00011 (tax rates); coupon and tax RPCs; fee due carries the discount
- [x] Gateway routes, OpenAPI, console, six languages, mocked e2e
- [ ] Outbound events, hosted invoice page, per-tenant dunning settings, card refunds of credit notes (not started)
