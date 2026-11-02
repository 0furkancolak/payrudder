# Tutorial: migrate from iyzico

You charge your customers through iyzico, with your own code or with iyzico's subscription product, and want Payrudder to keep the subscriptions, meter the usage and make the invoices. **iyzico can stay your payment provider**: Payrudder asks iyzico to charge a saved card, or sends the customer to iyzico's payment page ([iyzico](provider-iyzico.html)). What moves is the billing logic. Do every step in the sandbox first ([Test mode](test-mode.html)).

This tutorial is written from Payrudder's API and documents and from the compatibility matrix that records what iyzico's public documentation said (read on 2026-10-29 and 2026-11-02). **There is no importer**, **no iyzico account of any kind was used** to try it, and it has not been followed by a person migrating a real account. What the documentation does not settle is marked **UNVERIFIED**.

## What maps to what

| In iyzico or in your code | In Payrudder |
|---|---|
| Your plan and price list | A **package** and an immutable **version** with its prices (`POST /v1/plans`, `POST /v1/plans/{code}/versions`). Currencies iyzico takes through Payrudder: TRY, USD, EUR, GBP and CHF |
| The buyer you send with each charge | A **customer**, whose record is what Payrudder sends as the buyer (below) |
| A saved card | The customer's payment method, `cardUserKey:cardToken` (`PUT /v1/customers/{external_id}/payment-method`) |
| A recurring charge job | A **subscription** (`POST /v1/subscriptions`); Payrudder owns the renewal, the retries and the receipts, and asks iyzico to charge. iyzico's own subscription product is not used |
| Payment notifications | iyzico posts them to the address set in its merchant panel; the address is Payrudder's `/webhooks/iyzico`. Your own app's notifications come from Payrudder's [webhooks](webhooks.html) |
| Refunds and cancels | Credit notes, which the operator of the deployment issues and the tenant API reads (`GET /v1/credit-notes`); iyzico cancels a payment on the day it was taken and refunds it afterwards |
| Usage you counted yourself | A **meter** and **usage events** (`POST /v1/meters`, `POST /v1/events`) |

## 1. Connect iyzico's sandbox

In the console, **Integrations**, **Payments**, **iyzico** (the sandbox has its own button), enter the sandbox keys and the buyer details. Payrudder asks iyzico to accept the keys before it stores them.

## 2. Make the customer records complete

iyzico vets who pays, so a charge sends the customer's own record: name, e-mail, the tax id as the identity number (a TCKN or a VKN), the address with its city, postcode and country, and a mobile number in international form (`+90…`; a national spelling is refused). A field the customer lacks is taken from the buyer details of your account; when neither has it, **the charge is declined as incomplete**: nothing is charged and the retries read the customer again. So, before any charge:

- `PUT /v1/customers/{external_id}/contact` (the `phone`), `PUT /v1/customers/{external_id}/address` and `PUT /v1/customers/{external_id}/tax` ([Tax and e-invoices](tax-and-einvoices.html): the id is checked locally and only hinted at);
- `customer_type`: an individual or a business, which also decides the e-arşiv buyer.

## 3. Cards: what can and cannot come across

- **A card you already hold as a `cardUserKey` and a `cardToken`** is set as the customer's payment method, joined by a colon. Payrudder charges it without a 3-D Secure round trip (a non-3-D-Secure stored-card charge). **UNVERIFIED:** whether your iyzico account is enabled for that kind of charge; it needs an account to find out.
- **A card the customer saved on iyzico's own payment form** stays in their iyzico wallet: iyzico's documentation says `cardUserKey` and `cardToken` are not returned to you. Those customers cannot be moved by token and pay again on iyzico's checkout form, which does its own 3-D Secure.
- **Payrudder never holds a card number**, so it cannot register a new card with iyzico, and it has no page that takes one. A card is saved through iyzico's own page or by your own PCI-compliant page.

## 4. Move usage and subscribe, without charging twice

Replace each metering call with `POST /v1/events` (up to 500 per request): the meter's code, the customer's `external_id`, the quantity, the time it **happened** and an `idempotency_key` ([Idempotency](idempotency.html)). An event older than 35 days is rejected, so past usage cannot be imported: charge the periods up to the switch with your old code.

Then `POST /v1/subscriptions`. A billing period is a calendar month in the account's time zone, not an anniversary ([Usage metering](usage-metering.html)), so a customer you charged on the 17th is billed on Payrudder's calendar from now on. Whether a `start_time` in the past is accepted and what the first invoice then covers is **UNVERIFIED**: try it in the sandbox and close a period with `POST /v1/sandbox/close-period` to read the invoice.

A safe order for a live customer: create their Payrudder subscription to start when your old charge job's period ends, stop the old job for that customer at that moment, and after the first Payrudder invoice confirm that iyzico shows one charge, not two. A customer both systems charge is billed twice.

## 5. Notifications and the first payments

Set the notification address in the merchant panel (HTTPS). iyzico sends the first notification 10 to 15 seconds after a payment and repeats a failed one every 15 minutes, three times at most. Payrudder believes a notification only after it asked iyzico about the payment itself. **UNVERIFIED:** the V3 signature (`X-IYZ-SIGNATURE-V3`) is checked when the header is there, but iyzico documents that the feature must be enabled on your account, and which addresses iyzico sends from is not documented.

Run the first payments in iyzico's sandbox with its test cards and read the result in the console.

## What does not carry over

- **iyzico-run subscriptions.** They are not adopted; cancel them at iyzico only after the Payrudder subscription has paid.
- **Cards saved by customers on iyzico's forms** (above).
- **Instalments.** The checkout form offers only what the account's `enabled_installments` setting lists (default: one payment); stored-card charges are made in one payment.
- **Past payments and invoices.** They stay at iyzico and in your own system; Payrudder starts its own numbering. Turkish legal e-documents are a separate channel ([Tax and e-invoices](tax-and-einvoices.html)).
- **BKM Express and pre-authorisation.** Not offered through Payrudder (**UNVERIFIED** whether iyzico still offers the first).

## Before you switch off your old charge job

Run one period in the sandbox against the shapes of your real customers (a trial, a coupon, a plan change, a declined card), then one small real payment, and only then go live ([Go live](console-go-live.html)). Nothing in this repository has run against an iyzico sandbox or live account.
