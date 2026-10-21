# Phase 35 — Prepaid credits

## Scope

- [x] **Plan** (this file).
- [x] **Ledger core** (billing-svc module `credits`, ADR 0020 shape: `command/`, `query/`, `handler.go`).
  - [x] Migration `00020_credits.sql`: `credit_packs`, `credit_lots`, `credit_ledger`, all under RLS.
  - [x] sqlc queries (`credits.sql`), no SQL assembled at run time.
  - [x] Commands: create or withdraw a pack, grant (promotion or manual), consume, expire. Queries: packs, balance with its lots, ledger page.
  - [x] `BillingService` RPCs and error keys `billing.credits.*`; the sweeper writes off expired lots.
- [x] **API.** `GET/POST /v1/credit-packs`, `POST …/{id}/activate|deactivate`, `GET /v1/customers/{id}/credits`, `POST …/credits/grant`, `POST …/credits/consume`, `GET …/credits/ledger`; OpenAPI.
- [x] **Console.** A "Credits" tab: packs, a customer's balance and lots, grants, the ledger. Six languages.
- [x] **Tests.** Integration (packs, expiry order, idempotency, concurrent consumes, expiry sweep, one grant per payment), gateway handlers, mocked Playwright.
- [x] **Buying a pack.** A customer pays for a pack through a payment link (phase 34); when the payment is confirmed the credits are granted once (`GrantPurchase`, keyed by the payment). Built in [phase 36](phase-36-credit-purchase.md).
- [x] **Customer portal and low-balance event.** The customer's own page shows the balance and offers the packs; a `credits.low` webhook fires below a threshold the tenant sets. Built in [phase 36](phase-36-credit-purchase.md).

## Decisions

- **Credits are an integer unit, not money.** A tenant that sells "tokens" or "runs" prices a pack in a currency, and the application spends credits in its own unit. Money-denominated balances already exist for invoice credits (credit notes and re-rating); they stay a separate mechanism and are applied to invoices automatically. Credits are spent only through the API.
- **Lots, not a counter.** Every grant is a lot with its own optional expiry. A consume takes from the lot that expires first and lots that never expire go last, so a customer never loses credits that a later grant could have covered. A balance is the sum of the lots that have not expired; a lot past its time is not spendable even before the sweeper has written it off.
- **Append-only ledger.** Every grant, consume and expiry is a row with the lot it touched; the ledger of a customer adds up to the balance. A consume that spans two lots writes two rows under one idempotency key.
- **One writer per customer.** A transaction takes `pg_advisory_xact_lock` on the customer before it reads lots, so two concurrent consumes cannot spend the same credits. A refused consume (too few credits) rolls back and takes nothing.
- **Idempotency.** `consume` requires a key (body or `Idempotency-Key` header): a retried call returns the first result with `replayed: true`. The same key with another amount is `billing.credits.key_reused` (409). A grant may carry a key too. A purchase grants once per payment (unique `(tenant, source, source_ref)`).
- **Operator and tenant.** The console reaches the tenant API as the operator with `X-Quotify-Tenant`, as it does for customers and packages, so there are no separate admin routes.
- **Error status.** Too few credits is `FAILED_PRECONDITION`, which the gateway already answers as 409; the key `billing.credits.insufficient` is what a client tells it apart by.
- **Expiry runs in the existing sweeper**, on the system pool, 200 lots per pass.

## Limits

- No refund of credits when the payment that bought them is credited or voided; the operator withdraws them by hand for now.
- Credits are not part of the invoice saga: they do not pay an invoice.
- A `consume` is one call per spend; there is no hold-and-capture.
