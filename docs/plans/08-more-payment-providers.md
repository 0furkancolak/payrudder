# 08 — More payment providers (Stripe verified, others)

## The request
List 1 (3): add **other payment providers**.

## Where we are
#247 added a **Stripe** adapter (off-session PaymentIntent charge with `Idempotency-Key`, refunds, status by metadata search, invoice Checkout Session, `Stripe-Signature` webhooks, mode by key prefix) written from documentation and tested against a fake server. A console card with two fields. No other provider.

## Gaps
- The Stripe adapter is **UNVERIFIED** against Stripe: never run in test mode. Its customer-facing side is missing: a payment method (`pm_…`) must come from somewhere (Stripe Elements/Checkout setup mode); our hosted pages do not collect cards for Stripe, only an invoice Checkout redirect. Renewals off-session need a saved payment method and a customer object (`customer` is not sent today), SCA/3-D Secure authentication handling (`requires_action`) is treated as a decline.
- No review of Stripe API version pinning (`Stripe-Version`), rate limits, `Retry-After`, expandable objects, webhook endpoint management, or deprecated features.
- No sandbox (test mode) connection in the console (test keys connect as a sandbox-environment account later: not built).
- No decision about which other providers matter. Candidates for the owner's markets: **Mollie, Adyen, Checkout.com, PayTR, Param, Papara (Turkey)**, PayPal, Razorpay, and for the Arabic region **Tap, HyperPay, PayTabs, Moyasar**.

## Research first
1. With the owner: the target countries → which providers are needed first (coverage of local methods, fees, onboarding time).
2. For each candidate: API docs, test environment availability, SCA/3-DS model, tokenisation/vault model, subscription/MIT support, refund and webhook model, settlement currencies.
3. Extract the **adapter contract** from what 06/07 learn (what a provider must offer for card-on-file renewals, hosted checkout, refunds, webhook confirmation) and write it down so a new provider is an implementation of a checklist.

## Decisions needed
Which providers, in which order; test credentials for each; whether Payrudder supports card-on-file renewals (needs vault) or only provider-run subscriptions.

## Work
1. Contract doc (`docs/` + ADR) and a conformance test suite every adapter must pass (against a fake that behaves like the real one, plus an opt-in real sandbox run).
2. Stripe: customer + SetupIntent/Checkout setup mode for saving a payment method; off-session charge with `customer`; `requires_action` flow (email the customer a link); real test-mode run; webhook endpoint registration helper.
3. Next providers one at a time, each as its own plan file (`08a-…`), each with the contract tests and a sandbox transcript.

## Done means
Per provider: conformance suite green, sandbox transcript, console connect flow with test mode, docs page, CI nightly with a secret.

## Not verifiable here
Everything against live provider services.

## Risks
Each provider is a long tail (disputes, payouts, reconciliation). Scope each to "charge, refund, webhook, reconcile".
