# Paddle

Paddle Billing is a **merchant of record**: Paddle sells to your customer, collects the payment, works out and remits tax, issues the receipt and **runs the subscription itself** (renewals and retries are Paddle's). Payrudder keeps the catalogue, the customers, the usage and the invoices' side of the story and follows Paddle's notifications.

This page is written from the adapter's code and from the compatibility matrix that records what Paddle's public documentation said (read on 2026-10-29 and 2026-11-02). **Nothing here has run against a Paddle account**, not even a sandbox one; the last section lists what that leaves open.

## What you need

From your Paddle account (the dashboard sections are the ones the console names):

| Setting | Where in Paddle |
|---|---|
| API key | *Developer tools, Authentication*: a live key. A sandbox key starts with `pdl_sdbx_` |
| Webhook secret | *Developer tools, Notifications*: a destination for Payrudder, then its secret key |
| Usage product id | *Catalog*: the product Paddle uses for usage charges; its id starts with `pro_` |

## Connect it

In the console open the app, then **Integrations**, **Payments**, **Paddle**, and enter the three values (only an owner can connect). Payrudder asks Paddle to accept the key before it stores anything. The sandbox has its own button ("Test mode") and takes the keys of a Paddle sandbox account.

Paddle sends its notifications to `/webhooks/paddle/{tenant}/{environment}` on your deployment's public address, which is the address to give the destination in Paddle. The operator's Providers page shows that path with the tenant filled in; **how a tenant on a hosted deployment learns the address is UNVERIFIED**. A notification is believed only after its signature checks with the webhook secret.

## How it works

- **Catalogue.** A package version is made known to Paddle by a catalogue sync: the product once, and a recurring price for every interval and currency the version has a fee in. Prices are immutable at Paddle, which suits versions: a new version is new prices. A currency Paddle cannot bill is reported as skipped.
- **Checkout.** A customer subscribes on a checkout page that Paddle hosts. Paddle then renews, retries failed payments and sends the receipts (`SendsOwnReceipts`).
- **Tax.** Paddle works out the tax, so **Payrudder adds none to an invoice of a subscription Paddle runs** (the API says so under `PUT /v1/tax-rates`). See [Tax and e-invoices](tax-and-einvoices.html).
- **Changes at Paddle.** Plan or interval changes, pause and resume of a Paddle subscription are made at Paddle and Payrudder's copy follows. A change for the end of the period is refused: Paddle's documentation does not describe a scheduled change of the items (**UNVERIFIED**).
- **Moving a customer** between providers without charging twice is a provider migration; the old subscription stays until the customer has subscribed at the new place.

## Limits worth knowing

- A pending cancellation at Paddle is kept and shown, so a customer who cancelled still shows as active until it takes effect.
- Paddle's invoice link expires after an hour; Payrudder fetches it when it is asked for and never stores it.
- Rate limits: a `429` waits at least the `Retry-After` Paddle sends. The two limits on immediate charges (per hour and per day) cannot succeed until the next hour or day, and no wait is documented for them.

## What has not been verified

No Paddle account, sandbox or live, was used. Unverified in particular: whether a charge made on a subscription carries Payrudder's reference back in `transaction.completed` (the adapter relies on the price's `custom_data`; without it a notification cannot be matched to its charge and stays pending, it never charges twice), how long a charge takes (Paddle documents "longer than usual" without a figure) and the scheduled item changes above. Try the whole path in a Paddle sandbox before you go live.
