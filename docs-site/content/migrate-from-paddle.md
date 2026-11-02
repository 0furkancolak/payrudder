# Tutorial: migrate from Paddle Billing

You sell through Paddle Billing and want Payrudder to do the metering, the pricing and the invoices. First decide **which Paddle you are leaving**, because Paddle is a **merchant of record**: it sells to your customer, works out and remits the tax and issues the receipt. Do every step in the sandbox first ([Test mode](test-mode.html)).

This tutorial is written from Payrudder's API and documents and from the compatibility matrix that records what Paddle's public documentation said (read on 2026-10-29 and 2026-11-02). **There is no importer**, **no Paddle account of any kind was used** to try it, and it has not been followed by a person migrating a real account. What the documentation does not settle is marked **UNVERIFIED**.

## Two ways to leave

**A. Keep Paddle as the payment provider.** Payrudder meters usage and prices it; Paddle stays the merchant of record, collects the money, works out the tax and runs the subscription's renewals ([Paddle](provider-paddle.html)). Payrudder adds no tax to an invoice of a subscription Paddle runs. The fixed fee is Paddle's; the usage total is charged on the customer's Paddle subscription as a one-time charge.

**B. Stop using Paddle.** You become the seller. Payrudder runs the subscriptions and collects through another provider ([Stripe](provider-stripe.html), [iyzico](provider-iyzico.html) or bank transfer), and **tax becomes yours**: Payrudder is not a tax engine, it adds the rates you give it by the customer's country (`PUT /v1/tax-rates`) and works out no place of supply, threshold or exemption ([Tax and e-invoices](tax-and-einvoices.html)). Whether you may sell that way, and what registrations it needs, is for your accountant, not for this page.

In both ways **a customer's card does not move**: a card at Paddle stays at Paddle, and Payrudder has no way to read it. Each customer subscribes again, on a checkout page (Paddle's hosted one for A, the new provider's for B).

## What maps to what

| In Paddle Billing | In Payrudder |
|---|---|
| Product and prices | A **package** and an immutable **version** with its prices (`POST /v1/plans`, `POST /v1/plans/{code}/versions`). For A, a catalogue sync makes **new** products and recurring prices at Paddle from the version (one product, one price per interval and currency); your existing Paddle products are not reused. Paddle's prices cannot be edited, which suits versions: a new version is new prices |
| Customer, address, business | A **customer** with country, address and, for a business, `customer_type: business` and the tax id (`POST /v1/customers`, `PUT /v1/customers/{external_id}/address`, `PUT /v1/customers/{external_id}/tax`). For A, Payrudder finds the Paddle customer by e-mail and creates what is missing. Paddle keeps e-mails unique regardless of letter case, archived customers included, and refuses a duplicate: Payrudder then takes the id from the refusal and carries on |
| Subscription | A **subscription** (`POST /v1/subscriptions`); `provider` names the provider that sells it (`paddle` for A). Billed monthly or yearly |
| Discount | A **coupon** (`POST /v1/coupons`). On a subscription a provider runs, the discount is made at the provider |
| Transactions and receipts | For A, Paddle's. Payrudder fetches Paddle's invoice link when it is asked for (the link lasts an hour) and never stores it. For B, Payrudder's own invoices |
| Adjustments (refunds, credits) | Credit notes, which the operator of the deployment issues and the tenant API reads (`GET /v1/credit-notes`); for A, a refund is made at Paddle as an adjustment |
| Notification destination | For A, a destination in Paddle that points at Payrudder's `/webhooks/paddle/{tenant}/{environment}` address. Your own app's notifications come from Payrudder's [webhooks](webhooks.html) instead, with other names and payloads |
| Usage reporting | A **meter** and **usage events** (`POST /v1/meters`, `POST /v1/events`) |

## 1. Recreate the catalogue

Create the packages and their versions in the sandbox ([usage-based billing tutorial](tutorial-usage-billing.html)). A subscription renews monthly or yearly; a Paddle price with another interval has no equivalent. For A, connect a Paddle **sandbox** account first (Integrations, Payments, Paddle) and sync a version.

## 2. Create the customers

Create each customer with its country, language, type and, for a business, the tax id. For B, give each a payment path: a payment link or the customer's own page ([Let a customer pay](tutorial-payments.html)).

## 3. Move usage

Replace each usage report with `POST /v1/events` (up to 500 per request): the meter's code, the customer's `external_id`, the quantity, the time the usage **happened** and an `idempotency_key` ([Idempotency](idempotency.html)). An event older than 35 days is rejected, so **past usage cannot be imported**: let Paddle bill the periods up to the switch.

## 4. Subscribe, without billing twice

There is no way to attach a subscription that already exists at Paddle to a Payrudder subscription: `POST /v1/subscriptions` has no field for it. For A the customer subscribes again, on a Paddle checkout that Payrudder creates, and Payrudder links the new Paddle subscription through the `payrudder_subscription_id` it puts in the checkout's `custom_data`. **UNVERIFIED:** what Payrudder does with a notification about a Paddle subscription that does not carry that value (one you made yourself, or a destination shared with your old system). Give Payrudder its own notification destination, and in the sandbox first.

A safe order for a live customer:

1. Subscribe them in Payrudder to start **when their current Paddle period ends**.
2. In Paddle, set the old subscription to cancel at the end of the period. Paddle keeps a pending cancellation visible as a scheduled change and ends the subscription on its effective date.
3. After that date confirm that the first invoice was made and paid, and that Paddle sent no further charge.

If both are live for a customer for even one period, that customer is billed twice. **UNVERIFIED:** how long a charge takes at Paddle (its documentation says "longer than usual" and gives no figure) and whether a charge carries Payrudder's reference back in the completion notification (the adapter relies on it; without it a notification stays unmatched and pending, it never charges twice).

## 5. Switch your handlers

Register a webhook endpoint (`POST /v1/webhook-endpoints`) and handle Payrudder's events in place of Paddle's. Verify every delivery with the SDK ([Receive webhooks on your machine](tutorial-webhooks.html)). Deliveries are at least once and not in order: use the event `id`.

## What does not carry over

- **Cards and saved payment methods.** They stay at Paddle.
- **Past transactions, receipts and invoices.** They stay at Paddle; Payrudder starts its own numbering.
- **Existing Paddle subscriptions**, as above.
- **Paddle's tax work.** In B it is yours, as above. In A it stays Paddle's and Payrudder's invoice carries none of it: compare a first live charge with what you expect.
- **Paddle's own usage meters** and any Paddle events other than those the adapter reads (transactions completed and failed, subscriptions, adjustments); the rest are ignored.
- **Intervals other than monthly and yearly.**

## Before you switch off Paddle

Run one period in the sandbox with a few real customers' shapes (a trial, a coupon, a change in the middle of a period, a failed payment), then one small real payment, and only then go live ([Go live](console-go-live.html)). Nothing in this repository has run against a Paddle sandbox or live account.
