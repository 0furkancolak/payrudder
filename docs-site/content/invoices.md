# Invoices

An invoice is what a customer owes for a period or an event. Payrudder raises them; you read them (`GET /v1/invoices`, `GET /v1/invoices/{id}`), send the customer to pay them, and hear about them by webhook. **An issued invoice is never edited**: a correction is a credit note and, where needed, a new invoice.

## Kinds and lines

`kind` is one of `REGULAR` (a billing period's usage and, for a subscription billed in arrears, its fee), `FEE` (a fixed fee invoiced in advance for one period, or what a plan change moved), `CORRECTION` (a correction of a closed period) and `LINK` (made from an ad-hoc payment link). Each line has a `kind`:

| Line | What it is |
|---|---|
| `BASE_FEE` | the fixed fee, with the service period it covers (`service_from`, `service_to`) |
| `USAGE` | a meter's quantity for the period, priced by the package version the subscription was on |
| `LATE_USAGE` | usage that arrived after the period's cutoff and is billed on the next open invoice ([Usage metering](usage-metering.html)) |
| `CREDIT` | credit balance used to pay the invoice ([Credits and balances](credits.html)) |
| `CORRECTION`, `DISCOUNT`, `TAX` | a correction of earlier usage; a coupon's discount; tax at the account's rate for the customer's country |

Amounts are integers in the currency's minor unit, as strings (`total_minor`). The total is never negative: lines that come to less than zero (late-usage corrections larger than everything else on the invoice) leave the invoice at `0` and the rest becomes credit balance (`credit_surplus_minor`).

## Status

`status` is `draft`, `open`, `paid`, `uncollectible` or `void`. A **number** is given when the invoice is finalized (`INV-2026-000123`: the year, then a sequence without gaps for each year; a sandbox account's numbers are prefixed `SBX-`). Webhooks: `invoice.finalized`, `invoice.paid`, `invoice.voided` and `invoice.uncollectible` ([Webhooks](webhooks.html)). An invoice whose payment is declined is retried on a schedule before it is given up: [Failed payments and retries](dunning.html).

## Getting paid

- **A link the customer opens without signing in:** `POST /v1/invoices/{id}/links` makes it (valid 90 days unless you say otherwise, at most 365; the token is shown once) and `DELETE` on the same path stops every link. The page shows the invoice and its PDF, in the customer's language, and offers the payment methods the account has. See [Let a customer pay](tutorial-payments.html).
- **The PDF:** `GET /v1/invoices/{id}/pdf` returns a presigned link valid for five minutes.
- **What was paid:** `GET /v1/customers/{external_id}/payments` lists a customer's invoices with the payments on them (the rows the customer sees on their own page), and `GET /v1/payments` the account's charges and refunds, never summed across currencies.
- **What is coming:** `GET /v1/customers/{external_id}/upcoming-invoice` is an estimate of the next regular invoice, with no number, that moves with every event until the period closes.

## Credit notes

A credit note (`CN-2026-000007`) corrects a paid invoice: its amount goes to the customer's credit balance, which pays their next invoices, or back to the card through the provider that took the payment. The notes of an invoice never add up to more than the invoice. **The tenant API reads credit notes** (`GET /v1/credit-notes`, `GET /v1/credit-notes/{id}`, with `disposition` and, for a refund, `refund_state`); issuing one is an operator action, not a call with an API key.

Tax is added by the account's configured rates and nothing else; Payrudder is not a tax engine ([Tax and e-invoices](tax-and-einvoices.html)). In the console this is **Invoices**: [Invoices and payments](console-invoices.html).
