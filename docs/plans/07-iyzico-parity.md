# 07 — iyzico: full compatibility

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
