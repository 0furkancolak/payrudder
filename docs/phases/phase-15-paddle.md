# Phase 15 — Paddle

## Goal

A tenant can sell subscriptions through Paddle, the merchant of record: Paddle collects, renews and retries the fixed fee and works out tax, and Quotify bills
the usage on top through the customer's Paddle subscription. See [14-billing-platform.md](../14-billing-platform.md).

## Scope

**In:** the Paddle adapter (`pkg/payments/paddle`), per-tenant webhook URLs with signature verification, Paddle events into the ledger and into provider
subscriptions, hosted checkout, pending charges and ledger-first status, subscription ownership (`provider`, `provider_ref`, `owner`) in plans-svc and its use in
billing and in the charge command, gateway routes, compose.

**Out of scope:** choosing the provider by country and currency (phase 16: `CreateSubscription` takes the provider by name for now), moving a customer between providers
(phase 16), partial refunds, Paddle's `transaction.paid` and other events not listed below, the Helm chart.

## Decisions made while building

- **Paddle owns the fixed fee, Quotify bills the usage.** A provider-owned subscription's invoice has no `BASE_FEE` line; the usage total is charged as a one-time,
  non-catalogue charge on the customer's Paddle subscription (`POST /subscriptions/{id}/charge`, `effective_from: immediately`). Paddle's renewals appear in the ledger
  from their notifications, without an invoice.
- **A charge Paddle accepts is not a payment.** `Charge` returns `payments.PendingError`; the worker replies `PaymentErrored{PENDING}` (not retryable) and billing asks for the
  status. The status reads the ledger, which the `transaction.completed` / `transaction.payment_failed` notification fills in. Until then the query is retried, never answered "not found".
- **The key travels as custom data on the charge's price**, which is how a notification finds its invoice. Paddle does not dedupe charges by key, so a redelivered charge command
  asks first and the ledger is consulted before anything is charged again.
- **Webhooks are per account.** Paddle sends to `/webhooks/paddle/{tenant}/{environment}` (one notification destination per account); the signature (`Paddle-Signature`,
  HMAC-SHA256 of `ts:body` under the destination's secret, 5 minutes of tolerance, several `h1` accepted for rotation) is checked before anything is read. A failed check answers 401
  and does not use up the event id.
- **Subscription events keep the newest.** `provider_subscriptions` ignores an event older than the last applied; plans-svc ignores an older one too.
- **plans-svc follows the provider.** On `subscription.*` events carrying our subscription id, payments-worker tells plans-svc (`SyncProviderSubscription`), which links
  the provider's id, sets `owner = provider` (from the provider's capabilities) and applies the status (past_due, paused, canceled ends the subscription). If plans-svc cannot be
  reached the webhook answers 503 and Paddle retries.
- **A provider-owned subscription is not changed locally** (pause, resume, cancel, change plan are refused): the change would not reach Paddle.
- **Refunds are full refunds of a transaction** (an adjustment). The ledger records the refund when the command is accepted and again, as the same fact, when Paddle approves it.
- **From the Paddle docs and SDK types, to be confirmed in its sandbox** (the sandbox test is skipped without a key): that a non-catalogue price needs `product_id`, `tax_mode`, `unit_price` and
  a `quantity` range as sent; that price `custom_data` is echoed in `transaction.*` notifications; that one-time charges succeed when the subscription's currency equals the invoice's.

## Tasks

- [x] `pkg/payments/paddle`: charge (pending), refund, checkout, signature verification, notification mapping; fixture tests
- [x] Notification kinds (charge, refund, subscription, ignored), `PendingError`, `CheckoutProvider`
- [x] payments-worker: tenant webhook route, provider subscriptions, checkout and listing endpoints, ledger-first status, provider named by the command
- [x] plans-svc: `provider`, `provider_ref`, `owner`; `SyncProviderSubscription`; local changes refused for provider-owned subscriptions
- [x] billing-svc: no fixed fee for provider-owned subscriptions, the charge goes to the provider's subscription through the named provider
- [x] Gateway routes, OpenAPI, compose; docs
- [ ] Paddle sandbox run (needs the tenant's sandbox key, webhook secret, usage product and a price)

## Deliverables

- A customer subscribes through a Paddle checkout; Paddle's notifications link the subscription and keep its status; the usage invoice is charged on the Paddle subscription and settles from the notification.
