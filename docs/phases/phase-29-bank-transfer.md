# Phase 29 — Bank transfer (havale/EFT) and manual collection

## Goal

A tenant can bill customers who pay by bank transfer. Payrudder tells the customer where to send the money and with which reference, waits (days, not seconds) without marking the invoice failed, lets the operator confirm what arrived, and settles the invoice when the sum received reaches the total. The same operator can also settle an invoice that was never charged through Payrudder (cash, other).

## Scope

- Provider `banktransfer` (`pkg/payments/banktransfer`), registered next to iyzico and Paddle, in the providers catalogue and in payments-worker. Sandbox stays mock-psp only ([ADR 0011](../adr/0011-sandbox-environment.md)): a sandbox command never reaches it.
- Operator endpoints in payments-worker (and gateway routes + OpenAPI) to confirm a transfer, to read the transfer state of an invoice, and to reject or expire a pending transfer.
- A billing-svc RPC to record an offline payment (`bank_transfer|cash|other`) with who and when.
- Saga support for waiting: a new state `PAYMENT_PENDING`, polled on a schedule, with a timeout after which the normal dunning/uncollectible path applies.
- The tenant's bank details and the payment reference on the invoice PDF, the hosted invoice page and an "invoice issued, pay by transfer" e-mail.
- Console: the bank transfer account form, a "Record payment" action and the pending-transfer state on the invoice page; six languages.

## Non-goals

- Reading bank statements (open banking, MT940, CAMT.053, Turkish bank APIs). The operator confirms by hand; automatic matching is a later phase that can write the same ledger rows with source `reconciliation`.
- Refunding a transfer. There is no API for it: `Refunds=false`, a credit note that asks for a refund to the card fails with the provider's "no refund API" reason and the operator pays the customer back outside Payrudder (a credit note into the credit balance works as before).
- Overpayment as customer credit (see decisions).

## Decisions

| # | Decision | Why |
|---|---|---|
| 1 | The tenant's account settings are `bank_name`, `account_holder`, `iban`, `bic` (optional), `reference_template`, `currencies`, `payment_window_days`. They are stored like any provider account (sealed, `provider_accounts`). The IBAN is validated with mod 97, normalised (spaces removed, capitals) and its length is checked for the countries we know. | One account API for every provider; the console form posts to the same endpoint. |
| 2 | The settings hold no secret, but the account is stored sealed like the others. The public part (bank name, holder, IBAN, BIC, template, currencies, and whether it is the default account) is copied to plans-svc on save (`tenants.bank_transfer`, via `UpdateTenantSettings`) and cleared on delete. billing-svc reads it when it makes an invoice draft and freezes it in the snapshot. | The PDF, the hosted page and the mail are made by services that cannot read `payments_db`. Bank details are printed on invoices anyway. |
| 3 | The payment reference defaults to the invoice number (`{invoice_number}`). Other placeholders: `{invoice_id}` (first 8 hex digits). The result keeps letters, digits and ` -/.:`, at most 35 characters. `ChargePayment` gains `invoice_number`. | Customers type it into a bank form with a short, restricted description field. |
| 4 | `Charge` never calls anything. It returns `*payments.PendingError` carrying the reference. The handler records a `pending` ledger row (`provider_ref` = the charge's idempotency key, `detail` = the reference) and replies `PaymentErrored{PENDING}`, as for Paddle. `Capabilities`: Payrudder owns renewal and dunning, no hosted checkout, `Refunds=false`, `IdempotentCharges=false`. Because a charge has no side effect, a redelivered charge is simply answered again (the handler does not call `Status` first). | Reuses the existing "accepted, settles later" contract. |
| 5 | `QueryPaymentStatus` for a bank transfer reads the ledger: the sum of manual receipts of the invoice reaches the charge amount → `SUCCEEDED`; a `declined` row for this charge (rejected or expired) → `DECLINED`; otherwise the pending row is older than the payment window → the handler writes `declined` (`transfer_expired`) and answers `DECLINED`; otherwise a new status `PENDING`. No pending row at all → `NOT_FOUND` (the saga charges again, which is harmless). | The saga stays the only writer of invoice state. |
| 6 | New saga state `PAYMENT_PENDING` (no command outstanding, so no step deadline). Entering it from `PAYMENT_UNKNOWN` on `StatusPending` sends the "pay by transfer" mail once per charge and schedules the next poll (`PAYRUDDER_TRANSFER_POLL`, default 6 h, in `next_attempt_at`). A poll re-queries the status (`PollDue`). A poll nobody answers does not park the saga, it waits for the next one. `Retry` re-queries at once: payments-worker uses it as a nudge when a receipt completes the invoice, so settlement does not wait for the poll. A pending invoice can be voided. | Waiting days must not look like a failure or fill the dead-letter queues. |
| 7 | The payment window is `payment_window_days` in the tenant's account (default 14, max 90) and the deployment default `PAYMENTS_TRANSFER_WINDOW_DAYS` (YAML, default 14) when the account does not set one. When it passes, the status answer is `DECLINED` and the existing dunning schedule applies: the tenant's delays decide whether a new window (a fresh charge, a fresh mail) opens or the invoice becomes uncollectible. | Per-tenant and per-deployment, with no new scheduler. |
| 8 | Manual receipt (`POST …/invoices/{invoice}/transfer/receipts`): `amount_minor`, `currency`, `received_at`, `bank_reference`, `note`. It is idempotent on `(tenant, invoice, bank_reference)` (a repeat with the same amount answers 200 `duplicate=true`, with another amount it is a conflict). The currency must be the invoice's. The amount must be positive and at most the outstanding amount. Each receipt is a ledger row `kind=charge, status=succeeded, source=manual` (plus a `manual_receipts` row with the date, reference, note and actor). Partial receipts are recorded; the invoice is paid when the sum reaches the total. | Matches how bank statements look: one line per transfer. |
| 9 | **Overpayment is rejected** (`payments.transfer.overpayment`, HTTP 409) with the outstanding amount in the message; the operator records what the invoice needs and handles the rest outside Payrudder. Turning the excess into credit would need a billing-svc call that grants credit from outside a credit note, and a new source for `credit_balances`; not done in this phase. | Documented limit instead of a half-built mechanism. |
| 10 | Rejecting or expiring a transfer (`POST …/transfer/reject`, `outcome` = `rejected` or `expired`) writes a `declined` ledger row and nudges the saga, so dunning/uncollectible applies at once. Receipts already recorded stay (a partly paid invoice is not refunded). | The only exit before the window ends. |
| 11 | Settlement of an invoice not charged through Payrudder: `BillingAdminService.RecordInvoicePayment` (`method` = `bank_transfer|cash|other`, amount, date, reference, note, actor), table `invoice_payments`. Partial amounts are recorded, overpayment is rejected (`billing.payment.overpayment`), the invoice is paid when the sum reaches the total, through the saga event `OfflineSettlement` (allowed while the saga waits for or has failed a payment, and from uncollectible). It is idempotent on `(invoice, method, reference)` when a reference is given. The gateway takes the actor from `X-Payrudder-Actor` (the console sends the signed-in operator) and records `operator` when it is absent. | There is no per-operator identity in the API yet; the header is the audit trail's "who". |
| 12 | Reconciliation: receipts of one invoice are summed before they are compared with the invoice total, and an invoice paid through `RecordInvoicePayment` is not reported as "paid without charge". | Otherwise every transfer would be a discrepancy. |
| 13 | Error keys: `payments.transfer.*` and `billing.payment.*` in [error-keys.md](../error-keys.md). | ADR 0020. |

## Tasks

### Provider

- [x] `pkg/payments/banktransfer`: settings, IBAN validation (mod 97, length by country), normalisation, reference template, `Charge` (pending), `Status` (pending), `Refund` (`ErrUnsupported`), capabilities
- [x] `payments.ManualCollector` (optional interface: payment window) and the `ChargePayment.invoice_number` field
- [x] Register in `providers` and payments-worker; the account PUT validates the settings and mirrors the public part to plans-svc
- [x] plans-svc: `tenants.bank_transfer`, `UpdateTenantSettings`, `Tenant.bank_transfer`

### Saga and handler

- [x] Handler: pending ledger row on charge, ledger-based status with `PENDING`, expiry
- [x] Proto: `PaymentStatus.STATE_PENDING`
- [x] Saga: `PAYMENT_PENDING`, `StatusPending`, `PollDue`, `OfflineSettlement`, poll sweeper, mail, void; table test and the diagram in the billing-svc doc
- [x] Ledger migration: source `manual`, `manual_receipts`, `occurred_at` of an entry

### Operator endpoints

- [x] payments-worker module `transfers` (command/query/handler): receipt, state, reject/expire; nudge to billing-svc
- [x] Gateway routes, OpenAPI, `X-Payrudder-Actor`
- [x] billing-svc module `payments`: `RecordInvoicePayment`, `invoice_payments`, payments in `GetInvoice`
- [x] Reconciliation changes (decision 12)

### Documents

- [x] Snapshot, PDF, hosted invoice page and the `invoice_transfer.v1` mail show the bank details and the reference

### Console

- [x] Bank transfer form in the provider account settings
- [x] Invoice page: pending-transfer state, receipts, "Record payment", reject/expire; six languages
- [x] Console unit tests and mocked-gateway e2e like the neighbouring ones

### Tests and docs

- [x] Unit: IBAN, reference, partial/over payment, saga table, status from ledger
- [x] payments-worker integration: charge pending → receipt → paid (saga), partial, duplicate, overpayment, expiry
- [x] billing-svc integration: offline payment
- [x] `docs/error-keys.md`, ROADMAP row, [14-billing-platform.md](../14-billing-platform.md) "As built: phase 29", the billing-svc saga diagram

## Exit criteria

A transfer invoice goes `PAYMENT_UNKNOWN → PAYMENT_PENDING → PAID → COMPLETED` after a manual receipt, with the mail and documents showing the bank details; an unpaid one becomes declined after the window and follows dunning; a partial receipt leaves it pending; an overpayment is refused with an error key; an offline payment settles an invoice with who and when recorded; CI green.

## As built

- **Provider.** `pkg/payments/banktransfer` (settings, mod-97 IBAN with the length per country of the registry, BIC, reference template, `Charge` returns `*PendingError{Ref: reference}`), the optional
  `payments.ManualCollector` interface (the payment window), `ChargePayment.invoice_number`, the catalogue entry, and plans-svc `tenants.bank_transfer` (migration 00016, `UpdateTenantSettings`) which payments-worker fills when an
  account is stored, removed or made the default (a failed hand-over answers 502 and the PUT is repeated).
- **Handler.** The charge leaves a `pending` ledger row (reference = the charge's idempotency key, detail = the payment reference). The status query answers from the ledger as decided in 5; the expiry writes the
  `declined` row (`transfer_expired`), a rejection writes `transfer_rejected` or `transfer_expired` with the reason and the actor. The sum of receipts is checked against the *charge's* amount (the payments_db copy of the request), so
  the status query needs no call to billing-svc.
- **Saga.** `PAYMENT_PENDING` (the diagram in [billing-svc](../03-services/billing-svc.md) is regenerated from the table), `StatusPending`, `PollDue`, `OfflineSettlement`, `Retry` out of `PAYMENT_PENDING` and `PAYMENT_DECLINED`,
  `Void` out of `PAYMENT_PENDING`. `PAYRUDDER_TRANSFER_POLL` (6 h, at least 1 m). The dunning report lists pending invoices.
- **Receipts.** `transfers` module in payments-worker; the receipt row is `kind=charge, status=succeeded, source=manual` with `provider_ref = "{invoice id}:{bank reference}"`, plus `manual_receipts` (migration 00005). A lock on the
  invoice makes two operators confirming at the same time unable to overpay. The nudge to billing-svc is `RetryInvoiceSaga`; if it fails, the receipt stands and the response says `saga_notified: false`.
- **Offline payments.** `BillingAdminService.RecordInvoicePayment`, `invoice_payments` (migration 00012), `payments` in `GetInvoice`, `POST /admin/v1/invoices/{id}/payments`. An uncollectible invoice takes a payment recorded
  this way, not a transfer receipt (the saga no longer waits for it); the console picks the endpoint accordingly.
- **Documents.** The snapshot freezes the bank details when the draft is made (an invoice of a customer whose subscription is routed to `banktransfer`, or of one without a provider while the bank transfer account is the tenant's default
  account and takes the invoice's currency); the reference is worked out from the invoice number at finalization. PDF block, hosted page (six languages), `invoice_transfer.v1` mail (sent when the saga starts waiting and for each
  dunning retry), and the bank details repeated in `payment_failed.v1`.
- **Reconciliation.** Receipts of an invoice are summed; an invoice with a payment recorded by hand is not "paid without charge".
- **Console.** Structured bank transfer form (with the same IBAN check), the transfer card, receipts and recorded payments on the invoice page, "Record payment" / "Confirm bank transfer", reject/expire; error keys in six languages.
- **Credit-note refunds** of an invoice paid by transfer fail with "banktransfer has no refund API; refund the customer outside Payrudder"; a credit note into the credit balance works as before.

### Verified and not verified

- Verified: unit tests of all touched Go modules and the console; integration tests of billing-svc (saga waiting, polling, nudge, void, expiry into dunning, offline payments), payments-worker (charge, status, receipts, partial, duplicate,
  overpayment, expiry, rejection, late payment, account hand-over to plans-svc), plans-svc and the other touched services; the OpenAPI contract test; `make lint`; generated code is up to date; the console's mocked-gateway end-to-end suite.
- Not verified: nothing was run against a real bank or a real mail server; the full Docker end-to-end suites (`make e2e`, kind) are left to CI; the console was not driven against the real gateway.
- Not built: bank statement import, turning an overpayment into credit, per-operator identity (the actor is the `X-Payrudder-Actor` header, `console` from the console), routing rules in the console to `banktransfer` (the API accepts it).
