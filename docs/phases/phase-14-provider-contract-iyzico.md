# Phase 14 — Provider contract and iyzico

## Goal

A tenant can connect its own iyzico account, and live charges, refunds and iyzico's notifications for that tenant go through it and into one
ledger. See [14-billing-platform.md](../14-billing-platform.md).

## Scope

**In:** `pkg/payments` capabilities and registry, the iyzico adapter (`pkg/payments/iyzico`), sealed provider credentials (`pkg/secretbox`),
`payments_db` migration 00002 (accounts, ledger, notification inbox), per-tenant provider routing in payments-worker, its HTTP side (webhooks,
provider accounts, ledger), the gateway routes for them, compose wiring.

**Out of scope:** Paddle (phase 15), choosing the provider per customer by country and currency (phase 16), reports on the ledger (phase 17),
hosted checkout and card registration at iyzico (the customer's payment method token is entered as `cardUserKey:cardToken`), the Helm chart
(payments-worker has no Service there yet), per-customer buyer details (iyzico requires them; the account carries defaults).

## Decisions made while building

- **A provider declares what it does** (`Capabilities`): renewal and dunning ownership, refunds, hosted checkout, currencies, and whether a repeated
  idempotency key is honoured (`IdempotentCharges`). iyzico does not honour it, mock-psp does.
- **No second charge without asking.** When a charge command is redelivered (the worker may have died after calling the provider) and the provider
  is not idempotent, the worker asks `Status` first. If the status cannot be told, the command is retried later and nothing is charged.
- **`Status` never says "not found" on a guess.** The iyzico documentation does not give the error code for an unknown payment, so none is assumed:
  `not_found_codes` in the account settings lists the codes once the sandbox has shown them. Until then an unknown outcome needs an operator.
- **A notification is never believed.** Its body names a charge (iyzico's `paymentConversationId` is our idempotency key); the provider is then asked
  what the charge is, and only that answer reaches the ledger. The webhook signature scheme of API payments is not settled by the documentation, so
  Quotify does not depend on it.
- **The ledger is append-only and deduplicated on the fact** (provider, kind, reference, status): the command's answer and the notification are one row.
  A confirmation that disagrees with the recorded outcome is a second row, an error log and a `payment_discrepancies` count.
- **A ledger write never fails a command.** The provider has already been called; calling it again for a bookkeeping error could charge twice.
- **Credentials are sealed** with AES-256-GCM, bound to tenant, provider and environment; the key is `QUOTIFY_SECRETS_KEY`. Without it provider accounts and
  webhooks are off. Settings are validated by building the adapter and are never returned.
- **Live charges go to the tenant's default account**, else to `PAYMENTS_LIVE_PROVIDER`. Refunds and status queries follow the provider of the invoice's original charge.
- **iyzico facts taken from its documentation, to be confirmed in its sandbox** (the sandbox test is skipped, not passed, without credentials): `/payment/detail`
  accepting `paymentConversationId` alone; which `errorCode`/`errorGroup` values are system errors (`system_error_codes` in the settings) and which mean "not found".

## Tasks

- [x] `pkg/payments`: capabilities, owner, registry, notification parser and notifier
- [x] `pkg/payments/iyzico`: IYZWSv2 signing, charge with a stored card, refund, status, notification confirmation; fixture tests; sandbox test (skipped without keys)
- [x] `pkg/secretbox`
- [x] `payments_db` 00002: provider accounts, ledger, notification inbox
- [x] payments-worker: provider per tenant, ledger writes, redelivery guard, webhook and operator HTTP API
- [x] Gateway: `/webhooks/{provider}` (public), provider accounts and ledger (operator); OpenAPI
- [x] Compose; docs
- [ ] iyzico sandbox run (needs the tenant's sandbox keys and a stored sandbox card)

## Deliverables

- An operator stores a tenant's iyzico account; the tenant's live invoices are charged through it; the ledger shows the charge; iyzico's notification is recorded once or flagged when it disagrees.
