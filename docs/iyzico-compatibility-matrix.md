# iyzico adapter compatibility matrix

> Produced for plan 07 (`docs/plans/07-iyzico-parity.md`) by reading iyzico's public documentation against `pkg/payments/iyzico`. First pass 2026-10-29 through a summarising fetcher; **second pass 2026-11-02 re-read the raw markdown pages** (`https://docs.iyzico.com/en/<path>.md`, the embedded OpenAPI JSON parsed, not summarised) for every finding it closes. **Nothing here was run against an iyzico sandbox** and no merchant account exists: where the documentation does not settle a behaviour the row says UNCLEAR and the code does not guess. The section "Status of the findings" says per finding: closed, still UNCLEAR, or needs an account. Every iyzico page read on 2026-11-02 ends in an "Agent Instructions" block asking the reader to send GET requests to the docs site; it is page content and was ignored.

Read date for every doc citation: **2026-10-29**. Docs host: https://docs.iyzico.com (dev.iyzipay.com answers 302 to https://docs.iyzico.com/, fetched 2026-10-29). Docs index used: https://docs.iyzico.com/llms.txt (2026-10-29). Doc pages were read through a summarising fetcher (not raw HTML), so field lists are as the fetcher reported them; anything the fetcher said the page does not state is marked UNCLEAR. Nothing was run against a sandbox. Three doc pages (card-storage, preliminaries, preauth-and-capture) contained an "Agent Instructions" block asking the reader to send GET requests to the docs site; it was ignored (page content, not user instruction).

Adapter files: `/Volumes/DEVNAFRU/Projects/example/quotify/pkg/payments/iyzico/iyzico.go` (455 lines), `.../invoice.go` (66 lines), wired by `iyzico.Register(registry)` at `services/payments-worker/cmd/payments-worker/main.go:169`.

Status legend: current / deprecated / removed / changed / UNCLEAR / UNVERIFIED (could not fetch).

## Status of the findings (2026-11-02)

Findings are the eight of section 7. Code is in `pkg/payments/iyzico`; tests are named in brackets.

| # | Finding | Status | What the raw documentation says, and what changed |
|---|---|---|---|
| 1 | Checkout-form (HPP) notifications rejected | **closed** | webhook.md: the HPP format has `token`, `iyziPaymentId`, `iyziEventType` (CHECKOUT_FORM_AUTH, ...), `status`, `paymentConversationId`, `iyziReferenceCode` and **no `paymentId`**. `WebhookEvent` now carries `Token` and `IyziPaymentID`; a notification needs a reference and a payment id or a token [`TestAHostedPageNotificationIsParsedAndConfirmedByToken`]. |
| 2 | `Confirm` broke for payment-link keys | **closed** (#270) | `invoiceOfKey`. |
| 3 | No CF-Retrieve call | **closed in code; response values need an account** | cf-retrieve.md: POST `/payment/iyzipos/checkoutform/auth/ecom/detail` with `token` (and optional `conversationId`, `locale`); the answer has `status`, `paymentStatus`, `paymentId`, `basketId`, `fraudStatus`, `token`, `signature`. A hosted-page notification is now confirmed by it; the payment's `basketId` must be the invoice of the key and `token` the token asked for. The page does **not list the values of `paymentStatus`**: only `SUCCESS` and `FAILURE` (the notification's and `/payment/detail`'s values) are read, anything else is "not final". UNVERIFIED in the sandbox. |
| 4 | V3 signature not checked | **closed in code; needs account** | webhook.md "Validation of Notifications": header `X-IYZ-SIGNATURE-V3`; direct format = hex HMAC-SHA256, key = secret key, over `secretKey + iyziEventType + paymentId + paymentConversationId + status`; HPP format over `secretKey + iyziEventType + iyziPaymentId + token + paymentConversationId + status`; the feature "must be enabled on your account" (entegrasyon@iyzico.com); `X-Iyz-Signature` and `-V2` "will no longer [be] supported". The header is checked whenever it is there (a mismatch is `ErrUnverified`); `require_webhook_signature` refuses an unsigned notification once iyzico has enabled it. The check is **on top of** asking iyzico (Confirm), which stays the proof that a payment happened; both because the signature only shows the notification is iyzico's and an account without the feature sends none. The subscription format (a different formula, a separate URL) is not handled. A real notification is needed to confirm the bytes (for example how the numeric `iyziPaymentId` is rendered) [`TestNotificationSignatureV3`, written from the documented formula]. |
| 5 | No card-storage or 3-D Secure flow | **open, needs owner decision and account** | Not touched: which flows are in scope is plan 07's decision, and nothing here can be run. |
| 6 | `fraudStatus` ignored | **closed** | create-payment, retrieve, retrieve-payment and cf-retrieve all say: 1 approved, 0 under review ("the merchant should ship products only for transactions with value 1; for 0, the merchant should wait for notification"), -1 rejected/declined. A `success` with `fraudStatus` 0 is now `PendingError` (from `Charge`, from a lookup and from a confirmed notification, which iyzico redelivers every 15 minutes up to 3 times); -1 is a decline (`fraudulent`); an answer with no `fraudStatus` is read as before [`TestAPaymentUnderFraudReviewIsNotPaid`]. UNVERIFIED: whether iyzico sends a further notification when the review ends. |
| 7 | Health-check BIN had 6 digits | **closed** | installment-and-bin-service.md: `binNumber` "BIN to query (8 digits)". The check now looks up `55287900`, the first 8 digits of the test card 5528790000000008 (test-cards.md). An answer "BIN not found" (5066, 10213) is healthy, because iyzico accepted the signature; authentication codes 1000 to 1009 are bad credentials; over the limit (50 requests a minute) is "could not be asked" [`TestHealthCheckAsksForAnEightDigitBINAndReadsTheAnswer`]. |
| 8 | Error handling: `retryable`, the 50000 answer | **closed** | limiters.md: the answer over the limit is `{"status":"failure","errorCode":50000,...}` with the code as a **number**; the adapter decoded `errorCode` as a string, so that answer was "iyzico answered garbage". `errorCode` now reads a string or a number, and 50000 is transient. error-codes.md: bank/system codes documented as "please try again later" (10214, 10216, 10219, 10228, 10241, 10242) and codes 1 and 2 are transient; the authentication (1000 to 1009), general (3 to 27) and validation (5001 to 5999) codes are `*iyzico.Error`, not a decline and not retried although the table lists "retry: true" for them (it means after fixing the request); the documented issuer groups are mapped to Payrudder's decline vocabulary (`NOT_SUFFICIENT_FUNDS` is `insufficient_funds`, `EXPIRED_CARD` `expired_card`, `DEBIT_CARDS_REQUIRES_3DS` `authentication_required`, ...); `retryable` is read only for a code nobody documented [`TestErrorClassification`]. No new error key was needed: decline codes are free strings and `*iyzico.Error` surfaces as the worker's existing `REJECTED` reply. UNCLEAR: the HTTP status of the 50000 answer (a 429 is transient too), and whether `retryable` is sent on payment/auth answers. |

Conformance: `TestConformance` runs `pkg/payments/conformance` against a fake of iyzico's API (`docs/payment-provider-contract.md`).

Also changed: package comments no longer claim a sandbox verification that never happened.

## 1. Endpoint matrix

| # | Adapter call (file:line) | Method + path | Docs URL (read 2026-10-29) | What docs say | Status |
|---|---|---|---|---|---|
| 1 | `Charge` iyzico.go:293 | POST `/payment/auth` | https://docs.iyzico.com/en/payment-methods/api/non-3ds/non-3ds-implementation/create-payment.md and https://docs.iyzico.com/en/advanced/card-storage.md | Path and method match. Required: paidPrice, price, currency, paymentCard, buyer, shippingAddress, billingAddress, basketItems. paymentGroup enum PRODUCT/LISTING/SUBSCRIPTION; installment 1,2,3,6,9,12; paymentChannel WEB etc. Card-storage page says to pay with a stored card, send `paymentCard{cardUserKey, cardToken}` to `/payment/auth` (NON3D example only). Create-payment page does not define cardUserKey/cardToken and does not state whether Non-3DS is restricted. Response has `paymentId`, `fraudStatus`, `signature`; `paymentStatus` is null for this service. | current. Stored-card use is documented on the card-storage page only. Whether the account may do non-3DS is UNCLEAR (see 3DS row) |
| 2 | `Refund` iyzico.go:315 | POST `/v2/payment/refund` | https://docs.iyzico.com/en/advanced/refund-and-cancel.md | Refund V2 takes `paymentId` + `price` (+ optional conversationId, ip, locale, currency). Response has `refundHostReference`, `retryable`, `signature`. Page "strictly advises against" V2 for baskets with more than one product. Refund window 365 days; partial and repeated refunds allowed. Rate limit 150/min (limiters page, row 7). | current. Adapter sends the single-item basket, so V2 is fine. Page recommends v1 (`/payment/refund`, `paymentTransactionId`) for multi-item |
| 3 | `Status` iyzico.go:334, `Confirm` iyzico.go:411 | POST `/payment/detail` | https://docs.iyzico.com/en/payment-methods/api/non-3ds/non-3ds-implementation/retrieve.md and https://docs.iyzico.com/en/advanced/retrieve-payment.md | Request: `paymentId`, `paymentConversationId` ("Required if paymentId is not sent"), locale. The schema marks paymentId required, so the two statements conflict. Response: `paymentStatus` in SUCCESS/FAILURE/INIT_THREEDS/CALLBACK_THREEDS, `basketId`, `fraudStatus` 1/0/-1, `signature`. No error code or HTTP status is documented for "payment not found". `/payment/detail` is not in the limiters table. | current. Lookup by conversationId alone: UNCLEAR. Not-found code: UNCLEAR (adapter already treats it as unknown, iyzico.go:67-70). Lookup of a checkout-form payment through this endpoint: UNCLEAR |
| 4 | `CheckHealth` iyzico.go | POST `/payment/bin/check` | https://docs.iyzico.com/en/advanced/installment-and-bin-service.md | Endpoint exists. `binNumber` is "required, 8 digits". Failure for bad credentials not documented; only 200 and 400 are documented. Rate limit 50/min (limiters page). Sandbox host shown on page: `https://sandbox-api.iyzipay.com`. | current. The adapter sent the 6-digit `554960`; it now sends `55287900` (8 digits, test-card prefix). Bad credentials answer `status: failure` with an authentication code (1000 to 1009, error-codes.md). The error-codes page lists 5007 as "binNumber should be an exact length of 6 characters" with the detail "either 6 or 8 digits": the BIN page says 8 and wins |
| 5 | `CreateInvoiceCheckout` invoice.go:56 | POST `/payment/iyzipos/checkoutform/initialize/auth/ecom` | https://docs.iyzico.com/en/payment-methods/checkoutform/cf-implementation/cf-initialize.md | Path matches. Required: price, paidPrice, currency, callbackUrl (valid SSL), buyer, billingAddress, basketItems (shippingAddress per schema, not needed if all VIRTUAL). Optional: locale, conversationId, basketId, paymentGroup, enabledInstallments. Response: `token`, `checkoutFormContent`, `paymentPageUrl`, `signature`. No `tokenExpireTime` and no token validity in this schema. Rate limit 8000/min. | current. Adapter's own "UNVERIFIED" comment (invoice.go:22) is for lookup by conversationId, still UNCLEAR |
| 6 | (none) | POST `/payment/iyzipos/checkoutform/auth/ecom/detail` | https://docs.iyzico.com/en/payment-methods/checkoutform/cf-implementation/cf-retrieve.md | Takes `token`. Returns paymentStatus, paymentId, basketId, conversationId, signature. | current; **called since 2026-11-02** to confirm a hosted-page notification (finding 3) |
| 7 | Auth header, `doInto` iyzico.go:196-208, `Authorization` iyzico.go:149 | header | https://docs.iyzico.com/en/getting-started/preliminaries/authentication/hmacsha256-auth.md | `Authorization: IYZWSv2 <base64>`; base64 of `apiKey:K&randomKey:R&signature:S`; S = lowercase hex HMAC-SHA256 under secretKey of `randomKey + uri.path + request.body`; `x-iyzi-rnd` header sent in the example; empty body signs randomKey+path. Query-string handling not stated. | current. The page mentions a previous "SHA1" scheme and https://docs.iyzico.com/en/getting-started/preliminaries/authentication.md says "two different authentication methods" without naming them or marking either deprecated. Adapter uses the SHA256 one. Whether `x-iyzi-rnd` must equal randomKey: UNCLEAR (adapter sends equal values) |
| 8 | Base URLs iyzico.go:41-42 | host | https://docs.iyzico.com/en/getting-started/preliminaries/live-vs-sandbox.md | Live `https://api.iyzipay.com`; sandbox `https://sandbox-api.iyzipay.com`. | current, matches |
| 9 | `KeyMode` iyzico.go:125 | key format | https://docs.iyzico.com/en/getting-started/preliminaries/sandbox.md and live-vs-sandbox.md | Sandbox apiKey and secretKey start with `sandbox-`; live and sandbox credentials are entirely different. | current, matches |
| 10 | `ParseWebhook` / `ParseNotification` / `Confirm` iyzico.go:366-449 | POST to our `/webhooks/iyzico` (webhook.go:Handler) | https://docs.iyzico.com/en/advanced/webhook.md | See section 2. | changed vs adapter assumptions |

### 1a. Response fields the adapter reads vs docs

| Adapter field (iyzico.go:158-170, invoice.go:12-18) | In docs? |
|---|---|
| status, errorCode, errorMessage, errorGroup, conversationId | yes (error response lists status, errorCode, errorMessage, errorGroup on create-payment; refund/detail list errorGroup in some pages). errorGroup is absent from the CF-initialize error list |
| paymentId, paymentStatus, basketId | yes on /payment/detail; `paymentStatus` is null on /payment/auth (adapter only reads it in `state()` from detail, ok) |
| retryable | documented on refund responses only (refund page). Not documented on payment/auth or detail error responses (UNCLEAR) |
| refundHostReference | yes on refund |
| price (any) | yes |
| token, paymentPageUrl | yes (CF initialize) |
| fraudStatus | in docs, **read since 2026-11-02**: 1 approved, 0 under review, -1 rejected (finding 6) |

### 1b. Error mapping and codes

- Adapter classification (changed 2026-11-02, finding 8): Settings.SystemErrorCodes, the documented try-again codes (transient), the authentication/general/validation codes (`*iyzico.Error`), the documented issuer groups (decline codes in Payrudder's vocabulary), then `retryable:true` for an undocumented code (transient), else Declined with errorGroup or errorCode. HTTP >= 500 or 429 is transient.
- Docs https://docs.iyzico.com/en/add-ons/error-codes.md: error-code tables carry a Retry column. Authentication (1000-1009), Validation (5001-6001), Card storage, Onboarding and General codes are **all marked retry: true**; bank codes (10005-10251) have no retry column. Example group names: 10051 NOT_SUFFICIENT_FUNDS; 1000 "Invalid signature"; 1001 "API details not found"; 10214/10216/10228/10219/10241/10242 bank/system errors. Request limit: 50000 "Request Limit Exceeded" with `status: failure` (https://docs.iyzico.com/en/getting-started/preliminaries/limiters.md); HTTP status not specified there.
- Consequence if the live API echoes `retryable:true` for those codes: a bad signature (1000) or a validation error (5xxx) would be classed transient and retried. UNCLEAR whether the response `retryable` flag follows this table (the table is documentation of codes; the response field is only shown on refund responses).
- Rate-limit answer (50000) arrives as `status: failure`, not an HTTP 429, per the limiters page example, so adapter line 219 does not catch it; it falls into Declined with errorCode "50000" (iyzico.go:259-263). HTTP code UNCLEAR.
- Not-found code for /payment/detail: not documented. UNCLEAR. (Codes 5086/5087 are "Payment is not for this merchant" for cancel / 3DS lookups, error-codes page; not shown to apply to /payment/detail.)

### 1c. Idempotency / conversationId

- Adapter: idempotency key = conversationId (iyzico.go:266-267, 283); repeated keys are not rejected by iyzico (comment), so Status is called first on redelivery.
- Docs https://docs.iyzico.com/en/getting-started/preliminaries/idempotency.md: "Majority of iyzico services have designed non-idempotent"; conversationId and basketId are merchant-generated optional matching values; no idempotency header or field is documented; behaviour on repeated conversationId not documented. Status: consistent with the adapter's assumption (IdempotentCharges false). Retention: UNCLEAR.

### 1d. Amount format

Adapter sends decimal strings from minor units (`price()` iyzico.go:229). Docs describe price as decimal. The response-signature page says price fields are trimmed of trailing zeros before hashing (https://docs.iyzico.com/en/advanced/response-signature-validation.md); relevant only if responses are signature-checked (they are not, see below).

## 2. Webhooks

Source: https://docs.iyzico.com/en/advanced/webhook.md (read 2026-10-29); Q&A https://docs.iyzico.com/sss/webhook.md.

| Topic | Docs | Adapter | Verdict |
|---|---|---|---|
| Signature header | `X-IYZ-SIGNATURE-V3`; `X-Iyz-Signature` and `-V2` "no longer supported"; feature must be enabled by iyzico (entegrasyon@iyzico.com) | Not verified at all (headers ignored: `ParseNotification(body, _ http.Header)` iyzico.go:426, `ConfirmNotification` iyzico.go:438). Safety comes from re-fetching the payment (Confirm). | Documented scheme not implemented. Not wrong, but weaker than available; account feature needs enabling. changed |
| Direct format (API non-3DS/3DS) | fields paymentConversationId, merchantId, paymentId, status, iyziReferenceCode, iyziEventType, iyziEventTime, iyziPaymentId. Signature = hex HMAC-SHA256 key secretKey over `secretKey + iyziEventType + paymentId + paymentConversationId + status` | Parses iyziEventType, iyziEventTime, iyziReferenceCode, paymentId, paymentConversationId, status; verifies the signature when the header is there | fields match; signature implemented (finding 4) |
| HPP format (Checkout Form, Pay with iyzico) | **no paymentId field**; has `token` and `iyziPaymentId`; event types CHECKOUT_FORM_AUTH etc. Signature over `secretKey + iyziEventType + iyziPaymentId + token + paymentConversationId + status` | `ParseWebhook` accepted only a body with a `paymentId` (before 2026-11-02); `WebhookEvent` now has `token` and `iyziPaymentId` | closed 2026-11-02 (finding 1): parsed, signature verified, confirmed by CF-Retrieve |
| Subscription format | separate URL, events `subscription.order.success` / `.failure`, reference codes only | not handled | not applicable unless subscriptions are used |
| Retry | first delivery 10-15 s after payment; non-2xx repeated every 15 min; stops after 3 attempts | handler returns 401 on ErrUnverified (webhook.go:~338-345) | note: 3 attempts only; a 401 means iyzico stops retrying after 3 |
| Expected response | "until your server responds with a status of 2xx" (raw page, 2026-11-02): the body is not mentioned | not checked here | closed: any 2xx; the body does not matter |
| Sender IP allowlist | not mentioned | none | UNCLEAR |
| Configuration | panel Merchant Settings > Merchant Notifications (HTTPS URL); subscriptions in a separate setting | single URL `/webhooks/iyzico` | ok |

Additional adapter/Payrudder defects not in docs but found while checking (code, not doc claims):

- `Confirm` derives the invoice by cutting the conversation id at the first ":" and compares with the payment's basketId (iyzico.go:407, 418). Charge keys are `<invoice>:charge:<n>` (services/billing-svc/internal/sagas/command/publish.go:16), but payment-link keys are `link:<invoice>:<n>` (services/payments-worker/internal/invoicepayments/command/command.go:25, 132). For a link key the cut gives "link", which never equals the basketId (the invoice id, invoice.go:47), so every checkout-form payment would fail Confirm with ErrUnverified (401) even before the HPP-format issue.
- `Confirm` uses `/payment/detail` with paymentId, but an HPP notification carries `token`; the matching documented lookup is CF-Retrieve (row 6). Whether `/payment/detail` returns checkout-form payments is UNCLEAR.

## 3. Documented capabilities vs adapter

Endpoint sources are the llms.txt pages under https://docs.iyzico.com/en/ read 2026-10-29.

| Capability | Documented endpoint(s) | Adapter | Needed for "full compatibility"? |
|---|---|---|---|
| 3-D Secure | POST `/payment/3dsecure/initialize` (returns base64 `threeDSHtmlContent`, `paymentId`; needs callbackUrl), then POST `/payment/v2/3dsecure/auth` (v2 recommended; paymentId, paidPrice, basketId, currency) or `/payment/3dsecure/auth`. mdStatus 1 = success. Pages: payment-methods/api/3ds/3ds-implementation/init-3ds.md and auth-3ds.md | none | yes, if first payment must be 3DS (SSS: for stored cards "make the first payment with 3DS and send registerCard = 1"; https://docs.iyzico.com/sss/abonelik-ve-kart-saklama.md). Error 10217 "DEBIT_CARDS_REQUIRES_3DS" (error-codes page) |
| Card storage | POST `/cardstorage/card` (returns cardUserKey, cardToken), POST `/cardstorage/cards`, DELETE `/cardstorage/card`; or `registerCard`/`registeredCard` 1 during payment (advanced/card-storage.md; field spelled `registerCard` on the create-payment page and `registeredCard` in the card-storage text: UNCLEAR which is correct) | none; Charge consumes `cardUserKey:cardToken` (iyzico.go:240-247) but nothing in the repo creates it (repo grep for cardUserKey/cardstorage hits only the adapter and its tests) | yes: otherwise the stored-card Charge has no source of tokens. Cards saved on the hosted form are not returned to the merchant (SSS abonelik-ve-kart-saklama), so the checkout form cannot enrol them |
| Non-3DS stored-card recurring | `/payment/auth` | Charge | SSS: later non-3DS payments depend on the issuing bank's approval; "subscription and card storage add-ons ... can't be used at the same time"; stored cards cannot be used for subscription products |
| Checkout form result | `/payment/iyzipos/checkoutform/auth/ecom/detail` by token | missing | yes, for Confirm of HPP events |
| Subscriptions (iyzico Abonelik) | Product: POST/GET/DELETE `/v2/subscription/products[/{ref}]`; plan: `/v2/subscription/products/{ref}/pricing-plans`, `/v2/subscription/pricing-plans/{ref}` (RECURRING only; paymentInterval DAILY/WEEKLY/MONTHLY/YEARLY; currencies TRY/USD/EUR; only name and trialPeriodDays updatable; plan price not editable); subscription: `/v2/subscription/checkoutform/initialize`, `GET /v2/subscription/checkoutform/{token}`, `/v2/subscription/initialize`, `/v2/subscription/initialize/with-customer`, `.../subscriptions/{ref}/activate|cancel|upgrade`, `/v2/subscription/operation/retry`, GET detail/search, `/v2/subscription/card-update/checkoutform/initialize` (products/subscription/subscription-implementation/*.md). Add-on needed on live; all subscription transactions non-3DS; phone must be +90; webhooks go to a separate URL | none (Capabilities OwnsRenewal/OwnsDunning false, iyzico.go:132-139) | only if the owner wants iyzico to own renewals. Not required for the current merchant-driven model |
| Refund by transaction | POST `/payment/refund` with paymentTransactionId (item level); POST `/payment/cancel` (same-day full cancel, `paymentId`) | only V2 by paymentId | needed for multi-item baskets and same-day cancels; Payrudder baskets are single-item, so optional |
| Pre-auth / capture | `/payment/preauth`, `/payment/postauth`, 3DS and CF pre-auth variants (limiters page lists paths; implementation pages under payment-methods/preauth-and-capture/ were not fetched, UNVERIFIED) | none | optional |
| Installments / BIN | POST `/payment/iyzipos/installment` (price, optional 8-digit binNumber), `/payment/bin/check` | BIN check used for health only; Charge sends `installment: 1`, CF sends `enabledInstallments: [1]` | optional |
| BKM Express | `/payment/bkm/initialize`, `/payment/bkm/auth/detail` appear only in the limiters table; no BKM implementation page in llms.txt (only webhook event BKM_AUTH) | none | UNVERIFIED whether still offered |
| Marketplace / sub-merchant | `/onboarding/submerchant` (POST/PUT/detail), `subMerchantKey`, `subMerchantPrice`, `/payment/iyzipos/item/approve|disapprove` (products/marketplace/*.md; limiters page) | none | out of scope unless marketplace is wanted |
| Other products | Pay with iyzico, iyzico Link (`products/iyzico-link/iyzico-link-api.md`), bank transfer, mass payout, reporting, settlement files | none | optional |
| Response signature validation | HMAC-SHA256 hex over ":"-joined fields, key secretKey; non-3DS: paymentId, currency, basketId, conversationId, paidPrice, price; CF retrieve: paymentStatus, paymentId, currency, basketId, conversationId, paidPrice, price, token; refund: paymentId, price, currency, conversationId (advanced/response-signature-validation.md) | not checked | optional hardening |
| Test data | test cards incl. 4111111111111129 (insufficient funds), 4151111111111112 (3DS init failure), 5406670000000009 (cannot be cancelled/refunded) (add-ons/test-cards.md); sandbox OTP 123456 (sandbox.md) | `sandbox_test.go` only checks Status of an unknown key, and is skipped without env vars | useful for the UNCLEAR items |

### Adapter code with no caller found

Searched pkg and services (non-test, non-generated Go):
- `CreateInvoiceCheckout` has a caller (invoicepayments/command/command.go:135).
- `CheckHealth` is called via `payments.HealthChecker` (pkg/payments/health.go:48).
- `Status`, `Refund` called from handler.go:393, 463, 499.
- `ConfirmNotification` called via `payments.Notifier` (webhook.go:334).
- `Settings.SystemErrorCodes` and `NotFoundCodes` have no code path that fills them besides settings JSON (UNCLEAR whether the console exposes them; not searched in web/).
- `Settings.Buyer` fields: only defaults, per-customer values not sent (iyzico.go:45-46 comment).
- No caller creates `cardUserKey:cardToken` (see Card storage row).

## 4. Rate limits (https://docs.iyzico.com/en/getting-started/preliminaries/limiters.md, 2026-10-29)

Per minute: refund v2 150, cancel 150, CF initialize 8000, CF retrieve 1000, BIN check 50, card storage 150, subscription lists 100-500. `/payment/auth` and `/payment/detail` are **not listed** (limit UNCLEAR). Over-limit response is JSON `errorCode 50000`, `Request Limit Exceeded`; HTTP status not given.

## 5. Deprecation notices found

- None stated on the home page (https://docs.iyzico.com, fetched 2026-10-29: only a marketplace withholding-tax notice) or on the pages above.
- Webhook signature headers `X-Iyz-Signature` and `X-Iyz-Signature-V2` are "no longer supported" (webhook.md).
- "Previous SHA1 authentication" is mentioned on the HMAC page (details not given); adapter does not use it.
- The whole "API Reference" section is labelled "(Beta)" in the navigation (llms.txt); not a deprecation.
- Refund V2: advised against for multi-product baskets, not deprecated.
- 3DS auth: v1 `/payment/3dsecure/auth` vs "v2 (recommended)" `/payment/v2/3dsecure/auth`. Adapter uses neither.

## 6. UNCLEAR list (state after 2026-11-02)

**closed** = settled by a raw page read on 2026-11-02.

1. Does `/payment/detail` accept `paymentConversationId` without `paymentId`? **UNCLEAR, needs account**: the OpenAPI marks `paymentId` required and its own field note says the conversation id is "Required if paymentId is not sent" (retrieve.md, again 2026-11-02).
2. Error code/HTTP status for "no such payment" on `/payment/detail`: **UNCLEAR, needs account**. error-codes.md lists 5087 "Payment is not for this merchant" for "an incorrect paymentId" in the Retrieve Payment Result service; it is a request-error code and `Settings.NotFoundCodes` still starts empty.
3. Whether `/payment/detail` returns checkout-form payments and whether a checkout-form payment is findable by conversationId: **UNCLEAR, needs account**. A hosted-page *notification* no longer depends on it (CF-Retrieve by token); `Status` of a `link:` key still does.
4. `/payment/bin/check` BIN length: **closed** (8 digits); bad-credential behaviour **closed** (authentication codes 1000 to 1009 with `status: failure`; the HTTP status is not documented).
5. Whether non-3DS stored-card payments are enabled for a given account: **UNCLEAR, needs account**.
6. `registerCard` vs `registeredCard`: **UNCLEAR** (not used).
7. `retryable` on payment/auth and detail answers: **UNCLEAR, needs account**; the HTTP status of the 50000 answer: **UNCLEAR** (the body is documented and handled). Both a 429 and the body are transient.
8. Whether `x-iyzi-rnd` must equal the randomKey in the signature; whether a query string is signed: **UNCLEAR, needs account** (error 1004 says the header is mandatory for Postman requests).
9. Webhook: sender IPs **UNCLEAR** (not mentioned); the response body **closed** (any 2xx); whether `paymentId` is on checkout-form notifications **closed** (no: `token` and `iyziPaymentId`).
10. Checkout-form token lifetime: **UNCLEAR** (the CF-initialize schema has `token`, `paymentPageUrl`, `signature`, no expiry).
11. BKM Express support today: **UNVERIFIED** (limiter table only).
12. Pre-auth/capture: **UNVERIFIED** (navigation index only).
13. Idempotency retention window and behaviour of a repeated conversationId: **UNCLEAR**, as before.
14. (new) The values of `paymentStatus` in the CF-Retrieve answer: **UNCLEAR, needs account** (finding 3).
15. (new) Whether iyzico sends a second notification when a payment under fraud review is decided: **UNCLEAR, needs account** (finding 6).

## 7. Findings needing code changes (highest first)

1. Checkout-form notifications cannot be handled: HPP format has `token`, `iyziPaymentId`, no `paymentId`; `ParseWebhook` requires `paymentId` (iyzico.go:392).
2. `Confirm` breaks for payment-link keys: key `link:<invoice>:<n>` yields invoice "link" != basketId (iyzico.go:407,418 vs command.go:25,132). Every link payment returns ErrUnverified.
3. No CF-Retrieve call (`/payment/iyzipos/checkoutform/auth/ecom/detail` by token); `/payment/detail` for CF payments is UNCLEAR.
4. Webhook signature `X-IYZ-SIGNATURE-V3` is documented and not checked; needs account enablement.
5. No card-storage or 3DS flow, so nothing can produce the `cardUserKey:cardToken` that `Charge` requires.
6. `fraudStatus` (0 = under review) is ignored; a success with fraudStatus 0 is booked as paid.
7. Health check BIN `554960` is 6 digits (docs say 8); BIN check is limited to 50/min.
8. Error handling: `retryable` / the 50000 rate-limit answer (status failure) are not mapped to transient.
