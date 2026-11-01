# 07 — iyzico: full compatibility

## Status (2026-11-02)

**The five findings of the review addendum are closed in code; nothing verified against iyzico.** `docs/iyzico-compatibility-matrix.md` has the per-finding table ("Status of the findings"), written from the raw markdown pages re-read on 2026-11-02.

| | |
|---|---|
| Closed | `fraudStatus` (1 approved, 0 under review = `PendingError`, -1 rejected = a decline; read from `/payment/auth`, `/payment/detail` and CF-Retrieve), the checkout-form notification (`token` + `iyziPaymentId`, no `paymentId`) parsed and confirmed with the CF-Retrieve call, the V3 signature (both formulas of webhook.md; checked whenever the header is there, `require_webhook_signature` refuses an unsigned notification), the health-check BIN (8 digits; "BIN not found" is healthy; authentication codes are bad credentials), error handling (`errorCode` as a number, 50000 and the documented "try again later" codes transient, authentication/general/validation codes a typed `*iyzico.Error` instead of a decline, the documented issuer groups mapped to Payrudder's decline vocabulary). No new error key was needed (decline codes are free strings). |
| Still UNCLEAR | `/payment/detail` by conversation id alone, the "no such payment" code, whether it returns checkout-form payments, the `paymentStatus` values of CF-Retrieve, whether `retryable` is sent on payment/auth answers, the HTTP status of the 50000 answer, whether iyzico notifies again after a fraud review, the checkout-form token lifetime, `registerCard` vs `registeredCard`. |
| Needs an iyzico sandbox merchant | All of the above UNCLEAR, the V3 signature bytes (iyzico must enable the feature first: entegrasyon@iyzico.com), and every flow of "Work" 5. |
| Open, needs the owner | 3-D Secure, card storage (`cardUserKey`), the iyzico subscription product, per-customer buyer data (needs plan 05), instalments, same-day cancel and per-item refunds. Not started. |

Defaults chosen (not agreed): the response `signature` of iyzico's answers is **not** verified (optional hardening, documented in response-signature-validation.md; TLS and the signed request are the protection); the notification signature is checked when present and required only by `require_webhook_signature`; a payment under fraud review is pending, not declined. Added: the conformance suite run against a fake of iyzico (`docs/payment-provider-contract.md`).

## The request
List 1 (8) and list 2 (5): be exactly compatible with **iyzico**.

## Where we are
`pkg/payments/iyzico`: IYZWSv2 authorisation (HMAC-SHA256 of `randomKey + path + body`, header `x-iyzi-rnd`), non-3-D payment (`/payment/auth`), refund (`/v2/payment/refund`), payment detail for status and for webhook confirmation (the webhook is never believed: it names a payment and iyzico is asked), BIN-check health, sandbox host and `sandbox-` key prefix mode detection, buyer object from the account settings. Idempotency by `conversationId` (iyzico does not reject repeats, so a charge is retried only after `Status` says it did not happen).

## Gaps
- **Never run against an iyzico sandbox.** Only a fake server.
- Not covered: 3-D Secure (mandatory for most Turkish cards in practice: `/payment/3dsecure/initialize`, callback, `threeds` completion), the **hosted checkout form** (`/payment/iyzipos/checkoutform/initialize`) and its callback/retrieve flow, stored cards (`cardUserKey`, `cardToken`) and the subscription product (iyzico has its own subscription API: plans, pricing, `subscription/initialize`), instalments (`installment`) and BIN-based instalment display, refund types (cancel same day vs refund; partial refunds with `paymentTransactionId` per item), `basketItems` requirements (Turkish card rules need item lines), currency support (TRY, USD, EUR, GBP) and the exact decimal format (`"12.50"` strings, `price` and `paidPrice`), error codes mapping (`errorCode`, e.g. 10051 insufficient funds), IP and locale fields, rate limits, webhooks (the HMAC `X-IYZ-SIGNATURE-V3` header and event types) and the mismatch with our "ask iyzico" confirmation.
- Buyer data comes from one fixed settings object (the seller's), not the customer: iyzico's rules and fraud checks expect the real buyer (name, GSM, identity number, address, IP).
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
1. Matrix and fix list; implement missing flows in the order the owner picks (start: checkout form + callback, then 3-D Secure, then stored cards for renewals).
2. Customer-specific buyer data from the customer record (needs 05: identity number, address, GSM).
3. Webhook: implement the V3 signature check in addition to the "ask iyzico" confirmation; document why both.
4. Instalments, partial refunds, error-code mapping with our decline codes and user-facing text in six languages.
5. Sandbox transcript for each flow; recorded responses in `testdata/`.

## Done means
A matrix without "unclear", sandbox transcripts per flow, the sandbox test running in nightly, and the hosted page (04) completing a real sandbox payment.

## Not verifiable here
All of it against iyzico until credentials exist.

## Risks
3-D Secure and the checkout form redirect the customer: the return/callback URLs, CSP and idempotency of the callback need a design review (04).

## Review addendum (2026-11-01)
- `docs/iyzico-compatibility-matrix.md` exists (8 findings); finding 2 was fixed in #270. Open and not yet listed here: `fraudStatus` is ignored (a success with a fraud status of 0 is booked as paid); the checkout-form notification is rejected; there is no CF-Retrieve call; the health-check BIN is 6 digits against the documented 8; the V3 signature check is not implemented. **All five closed 2026-11-02** (see Status).
- iyzico has no webhook signature in the current adapter: the "ask iyzico" confirmation is the only trust. Keep it, add V3 verification as an extra check when the account has it enabled.
