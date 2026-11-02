# Stripe

Payrudder charges your customers' cards **on your own Stripe account**. Stripe is the payment provider; Payrudder stays the billing engine (subscriptions, usage, invoices, receipts and dunning are Payrudder's, not Stripe Billing's). Stripe is not a merchant of record here, so tax stays yours: see [Tax and e-invoices](tax-and-einvoices.html).

This page is written from the adapter's code and from the compatibility matrix that records what Stripe's public documentation said (read on 2026-10-29 and 2026-11-02). **Nothing here has run against a Stripe account**; the section at the end lists what that leaves open.

## What you need

- A Stripe account and a **secret or restricted key**: `sk_live_…` or `rk_live_…` for live, `sk_test_…` or `rk_test_…` for the sandbox's test mode.
- Which permissions a restricted key needs is **UNVERIFIED**; a secret key is what the checks below were designed around.

## Connect it

In the console, open the app, then **Integrations**, **Payments**, **Stripe** (only an owner of the account can connect). Paste the key.

1. **Payrudder asks Stripe before it stores anything.** The check is a read of the account's balance, which says whether the key is a live or a test key. A key whose prefix disagrees with Stripe's answer is refused, and so is a test key on live or a live key in the sandbox (`payments.account.livemode_mismatch`).
2. **The webhook endpoint.** Stripe tells Payrudder about payments and refunds through a webhook, and **no Stripe notification is believed until its signature can be checked**. The sheet's **Create the webhook endpoint** button asks Stripe to make the endpoint at Payrudder's own address for exactly the events it handles (`payment_intent.succeeded`, `payment_intent.payment_failed`, `checkout.session.completed`, `checkout.session.async_payment_succeeded`, `checkout.session.async_payment_failed`, `refund.updated`, `refund.failed`) and keeps the signing secret Stripe shows, sealed. Or add an endpoint yourself under *Developers, Webhooks* and paste its signing secret (`whsec_…`). Pressing the button again adds a second endpoint at Stripe; delete the old one there.
3. **The sandbox.** A test-mode key connects the app's sandbox to Stripe's test mode (the second button of the card). A sandbox charge whose payment method is a Stripe `pm_…` goes to Stripe; the `pm_sandbox_*` scenarios of [Test mode](test-mode.html) stay on the simulator.

## How a card is charged

- Payrudder charges a **saved** payment method with no customer present (a PaymentIntent confirmed off session). The customer's payment method is stored as `cus_…:pm_…` (the Stripe customer and the payment method, joined by a colon), or as a bare `pm_…`.
- A card is saved through Stripe's Checkout in setup mode. Today that page is made from the operator's side; **there is no button for the customer yet**. A payment method already saved on a Stripe customer can be set with `PUT /v1/customers/{external_id}/payment-method`.
- Every charge and refund carries an idempotency key, so a retry cannot charge twice. Stripe forgets a key after at least 24 hours, so a payment whose key is older than Payrudder's horizon for it is parked for a person instead of being charged again.
- Requests pin Stripe's API version to `2026-09-30.endive`, the first version of the Endive release, so a payload has the shape the adapter was written for whatever the account's default is.
- Stripe's own words decide what is retried: a `409`, `424`, `429` or `5xx` is retried, and so is anything with `Stripe-Should-Retry: true`; `Stripe-Should-Retry: false` stops it. A decline that needs the customer (`authentication_required`) is not retried blindly: the customer is told to come back.

## Limits worth knowing

- **ISK and UGX are refused.** Stripe wants them as two-decimal amounts ending in `00`, which Payrudder does not convert yet. For currencies with three decimals (BHD, JOD, KWD, OMR, TND) Stripe's page does not settle the rule: **UNVERIFIED**.
- **Disputes are recorded, not answered.** A dispute (Stripe's `charge.dispute.*` events) becomes a case you can read with `GET /v1/disputes` and see on the invoice's sheet: where it stands, the reason, the date your evidence is due, and what it cost. You can add notes and mark it as handled inside Payrudder. **Payrudder submits no evidence:** you answer the case at Stripe, before the date. Stripe's dispute fee is read from the balance transactions of the dispute; **UNVERIFIED** (Stripe's pages do not say where the fee shows), so a case may show no fee.
- A search at Stripe is eventually consistent, so "not found" can mean "not found yet"; Payrudder asks again before it would charge a second time.

## What has not been verified

Nothing was run against a Stripe account, test or live. The adapter is tested against a fake that speaks the wire format Stripe documents. Unverified in particular: the permissions of a restricted key, the behaviour of the webhook endpoint creation against a real account, and the three-decimal currencies. Run your first charges in test mode and read the result in the console before you go live.
