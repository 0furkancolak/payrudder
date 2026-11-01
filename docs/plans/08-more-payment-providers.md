# 08 — More payment providers (Stripe verified, others)

## Status (2026-11-02)

**The contract and the conformance suite exist; Stripe's documentation-closable findings are closed; no other provider; nothing verified against Stripe.** `docs/stripe-compatibility-matrix.md` has the per-finding table ("Status of the findings"), written from the raw markdown pages re-read on 2026-11-02.

| | |
|---|---|
| Done | Work 1: `docs/payment-provider-contract.md` (the checklist an adapter implements) and `pkg/payments/conformance`, a reusable suite (a charge that succeeds / is declined / hits a 5xx / a 429 / never answers, the key on the wire, a refund, a partial refund, capability flags against the interfaces, a duplicate and a forged notification) run against fakes of Stripe, Paddle, iyzico and mock-psp (`conformance_test.go` in each). Making it fail first was worth it: mock-psp's client did not treat a 429 as transient (fixed). |
| Stripe closed | 3 `Stripe-Version` pinned (`2026-09-30.endive`, overridable), 5 search consistency documented, 6 409 / 424 / `Stripe-Should-Retry` / `Stripe-Rate-Limited-Reason`, 7 in part (`authentication_not_handled` and `authentication_required` are one code), 8 deliberately ignored events with a test, 9 in part (ISK and UGX are two-decimal at Stripe and whole at Payrudder: refused instead of charged 100 times too little), 10 balance `livemode` against the key prefix, 12 metadata limits; 1 and 2 in part (`cus_...:pm_...` sends `customer`; `save_customer` makes an invoice Checkout create a Customer and keep the card). |
| Open (needs the owner / a place to store it) | A payment method has to come from somewhere (Checkout `mode=setup`, `PaymentMethodLink`, reading the customer and payment method back from the session and storing them), the `requires_action` flow (email the customer a link), classifying hard declines as no-retry in dunning, retrieving a PaymentIntent by its stored id instead of searching, the webhook endpoint registration helper, a Stripe sandbox test, the console test-mode connection. |
| Needs a Stripe account | Everything "verified": the transcript, `last_payment_error` field names, restricted-key permissions, whether a 424 was executed, the three-decimal currency rule. |

Default (not agreed, from the review addendum): no provider beyond Stripe, Paddle, iyzico and bank transfer until a sandbox exists. A new provider starts from the contract doc and a matrix, then its own `conformance_test.go`.

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
1. Contract doc (`docs/` + ADR) and a conformance test suite every adapter must pass (against a fake that behaves like the real one, plus an opt-in real sandbox run). **Done 2026-11-02** (`docs/payment-provider-contract.md`, `pkg/payments/conformance`); the opt-in sandbox run is each adapter's own `TestSandbox` and has never run; no separate ADR (the contract doc is the record).
2. Stripe: customer + SetupIntent/Checkout setup mode for saving a payment method; off-session charge with `customer`; `requires_action` flow (email the customer a link); real test-mode run; webhook endpoint registration helper.
3. Next providers one at a time, each as its own plan file (`08a-…`), each with the contract tests and a sandbox transcript.

## Done means
Per provider: conformance suite green, sandbox transcript, console connect flow with test mode, docs page, CI nightly with a secret.

## Not verifiable here
Everything against live provider services.

## Risks
Each provider is a long tail (disputes, payouts, reconciliation). Scope each to "charge, refund, webhook, reconcile".

## Review addendum (2026-11-01)
- `docs/stripe-compatibility-matrix.md` (12 findings) exists and #275 closed finding 4. Open: no `Stripe-Version` pin, no `customer`, no 409 / `Stripe-Should-Retry` mapping, nothing reacts to the 24-hour idempotency-key horizon (see plan 22 G2). **The pin, the 409 / `Stripe-Should-Retry` mapping and the `customer` for an attached method are done 2026-11-02**; the 24-hour horizon is plan 22's (#279). "Stripe verified" means "matrix closed and a conformance suite passes against the fake", not live.
- Default scope for "more providers" (not agreed): none beyond Stripe, Paddle, iyzico and bank transfer until a sandbox exists; build the conformance suite in `pkg/payments` that any new adapter must pass.
