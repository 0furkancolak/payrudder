# 07 — iyzico: full compatibility

## Status (2026-11-02)

**The five findings of the review addendum are closed in code; nothing verified against iyzico.** `docs/iyzico-compatibility-matrix.md` has the per-finding table ("Status of the findings"), written from the raw markdown pages re-read on 2026-11-02.

| | |
|---|---|
| Closed | `fraudStatus` (1 approved, 0 under review = `PendingError`, -1 rejected = a decline; read from `/payment/auth`, `/payment/detail` and CF-Retrieve), the checkout-form notification (`token` + `iyziPaymentId`, no `paymentId`) parsed and confirmed with the CF-Retrieve call, the V3 signature (both formulas of webhook.md; checked whenever the header is there, `require_webhook_signature` refuses an unsigned notification), the health-check BIN (8 digits; "BIN not found" is healthy; authentication codes are bad credentials), error handling (`errorCode` as a number, 50000 and the documented "try again later" codes transient, authentication/general/validation codes a typed `*iyzico.Error` instead of a decline, the documented issuer groups mapped to Payrudder's decline vocabulary). No new error key was needed (decline codes are free strings). |
| Still UNCLEAR | `/payment/detail` by conversation id alone, the "no such payment" code, whether it returns checkout-form payments, the `paymentStatus` values of CF-Retrieve, whether `retryable` is sent on payment/auth answers, the HTTP status of the 50000 answer, whether iyzico notifies again after a fraud review, the checkout-form token lifetime, `registerCard` vs `registeredCard`. |
| Needs an iyzico sandbox merchant | All of the above UNCLEAR, the V3 signature bytes (iyzico must enable the feature first: entegrasyon@iyzico.com), and every flow of "Work" 5. |
| Open, needs the owner | Card **registration** (a card number would pass through Payrudder: see the first default below), the iyzico subscription product, who runs renewals, a console field for `enabled_installments`, an instalment chooser on the invoice page, endpoints for listing and deleting a customer's stored cards. |

**Second step, flows (2026-11-02, branch `feat/iyzico-flows`).** Every behaviour below was written from the raw pages re-read that day (URLs and dates in the matrix, section "Status of the flows") and is tested against `httptest` fakes only; **none of it has run against iyzico**.

| | |
|---|---|
| Closed in code | (1) The buyer is the customer record, never a fixed object: name, email, `phone`, `tax_id`, address, country and the payer's IP; a field the customer lacks comes from `Settings.Buyer` only if the seller configured it, and what is still missing is named (`BuyerIncompleteError`) before iyzico is asked. (2) 3-D Secure with the customer's **stored card**: `StartThreeDS`, the bank's page served as a sandboxed document, the callback confirmed (`mdStatus`, v2 auth once, `/payment/detail` on a lost answer). (3) The checkout form end to end: the callback is a gateway route, the return is confirmed with CF-Retrieve (token, basket, price, currency), ledger and billing are settled once, the page shows paid, processing or failed. (5) `Installments` query, `enabled_installments` for the form. (6) A refund of the whole payment on its day is a cancel, else a refund; the #279 guard is untouched. (7) More documented bank codes map to decline words, and the invoice page tells the payer why in six languages. Conformance: `Harness.Return` and `Harness.Buyer`. |
| Decided (default, not agreed) | **Payrudder holds no card number.** Both documented ways to register a card at iyzico (`POST /cardstorage/card`, `registerCard` in a payment) take the number, and the checkout form stores nothing for the merchant (sss/abonelik-ve-kart-saklama), so card registration (Work 4) and 3-D Secure with a typed card are **not built**: a payment page of Payrudder that took numbers would put every tenant's payers in Payrudder's PCI scope. The `cardUserKey:cardToken` of a customer keeps coming from whoever holds the number, through `PUT /v1/customers/{id}/payment-method`; Payrudder lists and deletes the cards (adapter only), charges them and authenticates them. The owner may overturn this: it is a decision about scope and compliance, not a missing call. |
| Needs an iyzico sandbox merchant | All of it, in particular the form encoding of the callback, whether the 3-D Secure initialise call takes a stored card, a second auth call for a finished payment, the checkout form's token lifetime, the "same day" of a cancel, the `country` and non-`+90` phone values, instalment interest on the form. |

Defaults chosen (not agreed): a charge whose customer lacks what iyzico needs is **declined** as `buyer_incomplete:<fields>` (not answered with an error: the saga would wait for a status that never comes, and dunning, which reads the customer again on every retry, is what completes it once the seller fills the record in), and a hosted start answers `422`; a cancel is tried only for the whole payment on its day in Turkey's clock; the response `signature` of iyzico's answers is **not** verified (optional hardening, documented in response-signature-validation.md; TLS and the signed request are the protection); the notification signature is checked when present and required only by `require_webhook_signature`; a payment under fraud review is pending, not declined. Added: the conformance suite run against a fake of iyzico (`docs/payment-provider-contract.md`).

## The request
List 1 (8) and list 2 (5): be exactly compatible with **iyzico**.

## Where we are
`pkg/payments/iyzico`: IYZWSv2 authorisation (HMAC-SHA256 of `randomKey + path + body`, header `x-iyzi-rnd`), non-3-D payment (`/payment/auth`), refund (`/v2/payment/refund`), payment detail for status and for webhook confirmation (the webhook is never believed: it names a payment and iyzico is asked), BIN-check health, sandbox host and `sandbox-` key prefix mode detection, buyer object from the account settings. Idempotency by `conversationId` (iyzico does not reject repeats, so a charge is retried only after `Status` says it did not happen).

## Gaps
- **Never run against an iyzico sandbox.** Only a fake server.
- Not covered **(2026-11-02: the flows below are built for what needs no card number; see Status)**: 3-D Secure (mandatory for most Turkish cards in practice: `/payment/3dsecure/initialize`, callback, `threeds` completion), the **hosted checkout form** (`/payment/iyzipos/checkoutform/initialize`) and its callback/retrieve flow, stored cards (`cardUserKey`, `cardToken`) and the subscription product (iyzico has its own subscription API: plans, pricing, `subscription/initialize`), instalments (`installment`) and BIN-based instalment display, refund types (cancel same day vs refund; partial refunds with `paymentTransactionId` per item), `basketItems` requirements (Turkish card rules need item lines), currency support (TRY, USD, EUR, GBP) and the exact decimal format (`"12.50"` strings, `price` and `paidPrice`), error codes mapping (`errorCode`, e.g. 10051 insufficient funds), IP and locale fields, rate limits, webhooks (the HMAC `X-IYZ-SIGNATURE-V3` header and event types) and the mismatch with our "ask iyzico" confirmation.
- Buyer data comes from one fixed settings object (the seller's), not the customer: iyzico's rules and fraud checks expect the real buyer (name, GSM, identity number, address, IP). **Closed 2026-11-02** (customer record, explicit fallback; see Status).
- Sandbox card numbers and test flows are not scripted.

## Research first
1. Read iyzico's developer docs end to end for the endpoints above (docs.iyzico.com), the official SDKs (Java, Node, PHP, Python, .NET) to see the exact request shapes, the changelog and the "important notices" for deprecated endpoints; note dates.
2. Matrix as in 06: call/field/event → docs → status → test.
3. Decide with the owner which payment experiences are in scope: server-side non-3DS (rarely accepted), 3-D Secure, hosted checkout form, subscriptions (iyzico-owned renewal) — each is a different adapter shape (`OwnsRenewal`).

## Decisions needed
- An **iyzico sandbox merchant** (API key, secret) and the sandbox test cards.
- Which flows are required (3-D Secure and the checkout form are what Turkish customers actually use).
- Whether iyzico runs renewals (their subscription product) or Payrudder does (card-on-file with `cardUserKey`).

## Work
1. Matrix and fix list; implement missing flows in the order the owner picks (start: checkout form + callback, then 3-D Secure, then stored cards for renewals). **Done 2026-11-02 except registration** (no order was picked: all three were built; stored cards are listed, deleted, charged and authenticated, not registered).
2. Customer-specific buyer data from the customer record (needs 05: identity number, address, GSM). **Done 2026-11-02** (plan 05 added `phone` and `tax_id`).
3. Webhook: implement the V3 signature check in addition to the "ask iyzico" confirmation; document why both.
4. Instalments, partial refunds, error-code mapping with our decline codes and user-facing text in six languages. **Done 2026-11-02**, except an instalment chooser on the invoice page and a console field for `enabled_installments`.
5. Sandbox transcript for each flow; recorded responses in `testdata/`. **Not done**: no sandbox exists.

## Done means
A matrix without "unclear", sandbox transcripts per flow, the sandbox test running in nightly, and the hosted page (04) completing a real sandbox payment.

## Not verifiable here
All of it against iyzico until credentials exist.

## Risks
3-D Secure and the checkout form redirect the customer: the return/callback URLs, CSP and idempotency of the callback need a design review (04).

**Built 2026-11-02, the design the review asked for** (04): the callback is `POST /i/{token}/return/{key}` (portal twin), public and rate-limited; the form is never believed (payments-worker asks iyzico, binds the token or payment id, the basket, the amount and the currency to the attempt); the answer is a redirect to the invoice page whatever happens; a repeat is answered from the ledger; the bank's page is a sandboxed document with its own CSP. **Known limit:** the 3-D Secure auth call is made once. If it fails with a system error, or its answer is lost and `/payment/detail` cannot say, the payment is left unfinished, the payer is told it is processing, and the invoice offers payment again after 30 minutes; no other code can finish it, since iyzico posts to the callback once. Whether iyzico repeats a callback is UNVERIFIED.

## Review addendum (2026-11-01)
- `docs/iyzico-compatibility-matrix.md` exists (8 findings); finding 2 was fixed in #270. Open and not yet listed here: `fraudStatus` is ignored (a success with a fraud status of 0 is booked as paid); the checkout-form notification is rejected; there is no CF-Retrieve call; the health-check BIN is 6 digits against the documented 8; the V3 signature check is not implemented. **All five closed 2026-11-02** (see Status).
- iyzico has no webhook signature in the current adapter: the "ask iyzico" confirmation is the only trust. Keep it, add V3 verification as an extra check when the account has it enabled.
