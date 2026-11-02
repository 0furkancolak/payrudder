# Tutorial: migrate from Stripe Billing

You use Stripe for subscriptions, invoices and metered usage and want Payrudder to do that part. This page maps the pieces, gives the order that keeps you from billing anyone twice, and says what **does not carry over**. Do every step in the sandbox first ([Test mode](test-mode.html)).

**Stripe can stay your payment provider.** Payrudder can charge cards on your own Stripe account ([Stripe](provider-stripe.html)), so what moves is the billing logic, not the money path. There is **no importer**: nothing reads your Stripe account for you, and you create the objects below with the API (or the console).

This tutorial is written from Payrudder's API and documents. It does not describe Stripe's side beyond the names of the things you have there, and it has not been followed by a person migrating a real account.

## What maps to what

| In Stripe Billing | In Payrudder |
|---|---|
| Customer | A **customer**, under an `external_id` of your choosing (your own user id is the usual choice) |
| Product and price | A **package** (plan) and an immutable **version** that holds the prices (`POST /v1/plans`, `POST /v1/plans/{code}/versions`) |
| Meter and meter events | A **meter** and **usage events** (`POST /v1/meters`, `POST /v1/events`) |
| Subscription | A **subscription** (`POST /v1/subscriptions`), billed monthly or yearly |
| Invoice | An **invoice** Payrudder makes when the period closes; it numbers its own |
| Coupon and promotion code | A **coupon** and **promotion codes** (`POST /v1/coupons`) |
| Tax rates | `PUT /v1/tax-rates`, one rate per customer country ([Tax and e-invoices](tax-and-einvoices.html)) |
| Webhook events | Payrudder's [webhooks](webhooks.html): other names and payloads, so your handlers change |
| Hosted invoice page and customer portal | The hosted invoice page and the customer's own page (`POST /v1/customers/{external_id}/portal-links`) |
| Checkout / payment links | Payment links (`POST /v1/payment-links`) |

## 1. Connect Stripe's test mode

Connect your Stripe **test** key to the app's sandbox ([Stripe](provider-stripe.html)). A sandbox charge with a Stripe `pm_…` payment method then goes to Stripe's test mode; the `pm_sandbox_*` tokens keep using the simulator.

## 2. Recreate the catalogue

For each Stripe product that you sell, create a package, then a version with the prices. Read the [usage-based billing tutorial](tutorial-usage-billing.html) for the calls.

- A version **cannot be edited**; a change of price is a new version, and existing subscriptions stay on the version they started with.
- A subscription renews **monthly or yearly** (`SUBSCRIPTION_INTERVAL_MONTH`, `SUBSCRIPTION_INTERVAL_YEAR`). A Stripe price with another interval has no equivalent.
- The fixed fee is billed in advance by default; `fee_timing: FEE_TIMING_ARREARS` bills it with the month's usage.

## 3. Create the customers, with their cards

Create each customer (`POST /v1/customers`) with the country, the language (`locale`), the type (`individual` or `business`) and, for a business, the tax id ([Tax and e-invoices](tax-and-einvoices.html)).

To keep charging the card a customer already saved at Stripe, set their payment method to the Stripe customer and payment method joined by a colon, `cus_…:pm_…` (`PUT /v1/customers/{external_id}/payment-method`).

- **Consent.** Stripe's documentation says a card charged off session needs the customer's stored consent (a mandate). Keep the evidence you already have, and check your own terms: Payrudder cannot tell whether it covers charges made through another system. If you use Payrudder's [contracts](contracts.html), customers accept the seller's agreements where they pay.
- **UNVERIFIED:** that a live `cus_…:pm_…` saved by Stripe Billing is accepted and charged by Payrudder as it is. The sandbox path was built against Stripe's test mode and a fake; no real account was used. Try one customer with a small amount first.

## 4. Move usage

Replace each metering call with a call to `POST /v1/events` (up to 500 events per request): the meter's code, the customer's `external_id`, the quantity and the time the usage **happened**, plus an `idempotency_key`, which a live key must send. Use the id your system already has for the occurrence (an order or request id) so a retry counts once ([Idempotency](idempotency.html)).

An event older than 35 days is rejected (`EVENT_TOO_OLD`, see [Errors](errors.html#event-codes)), so **past usage cannot be imported**: invoice the periods up to the switch in Stripe, and let Payrudder meter from the first period that it bills.

## 5. Subscribe, without billing twice

`POST /v1/subscriptions` with the customer, the plan and `start_time`. Whether a start time in the past is accepted, and what the first invoice then covers, is **UNVERIFIED** here: try it in the sandbox and close a period with `POST /v1/sandbox/close-period` to read the invoice.

A safe order for a live customer:

1. Create their Payrudder subscription to start **when their current Stripe period ends**.
2. Set the Stripe subscription to cancel at the end of the period (in Stripe).
3. After that period, confirm in Payrudder that the first invoice was made and paid, and that Stripe sent no further charge.

Do it for a handful of customers first, then in batches. If both systems are live for a customer for even one period, that customer is billed twice.

## 6. Switch your webhook handlers

Register an endpoint (`POST /v1/webhook-endpoints`) and handle the events you used from Stripe's (`invoice.paid`, `invoice.finalized`, `subscription.cancelled`, `credit_note.created`, …) in their Payrudder form. Verify every delivery with the SDK ([Receive webhooks on your machine](tutorial-webhooks.html)). Deliveries are at least once and not in order: use the event `id`.

## What does not carry over

- **Past invoices and payments.** They stay in Stripe; Payrudder starts its own numbering.
- **A customer's stored cards by themselves.** Only the cards you set as above are used; a customer who has none is asked to pay through a payment link or the hosted page.
- **Anything of Stripe that Payrudder does not do:** disputes are recorded (with a note and a "handled" mark of your own) but answered at Stripe, never from Payrudder, and a tax engine does not exist (Stripe Tax is not replaced; you give Payrudder the rates).
- **Subscription periods other than monthly and yearly.**

## Before you switch off Stripe Billing

Run one full period in the sandbox against a few real customers' shapes (a trial, a coupon, a mid-period change, a failed payment), compare the invoices with what Stripe would have made, and only then go live ([Test mode](test-mode.html), promote).
