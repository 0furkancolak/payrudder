# Phase 36 — Buying credit packs

## Goal

An end customer of a tenant can **buy a credit pack** (phase 35) and get the credits once the payment is confirmed. The tenant hands out a purchase link, or the customer buys from their own portal page. Money stays in the existing path: a payment link (phase 34) makes an invoice, the invoice's saga collects it, and when the saga reaches `PAID` the credits are granted in the same transaction. The gateway shows pages and forwards requests; it never decides a price and never grants anything.

## Scope

**In:**

1. **`credit_pack` payment links.** `POST /v1/payment-links` with `kind: credit_pack`, `pack_id`, `customer_external_id` and an optional `quantity` (1 to 1000, default 1). The amount, currency and credits are the pack's (times the quantity) and are frozen on the link when it is made; a request that names an amount, currency or description is refused (`billing.payment_link.bad_kind`). The customer is required, the pack must exist and be on sale, and the same idempotency key as an ad-hoc link applies (the pack and quantity are part of the request it is bound to).
2. **Hosted page.** `/i/{token}` of such a link shows the pack: name, credits, validity, quantity, price. *Continue* makes the invoice (kind `LINK`, line "Pack x2") exactly as for an ad-hoc link.
3. **Grant on paid.** `saga.linksPaid` already marks the links an invoice was paid through. For a `credit_pack` link it now calls `credits.PurchasePaid`, which inserts the lot (`GrantPurchase`, `source = purchase`, `source_ref` = the invoice id, expiry = now + the pack's validity) and the ledger row, and records `credits.purchased`, all in the saga's transaction. A provider webhook, an operator's offline settlement and a mock payment all end in that one place.
4. **Pack validity.** `credit_packs.validity_days` (1 to 3650, none means the credits never expire). The lot of a purchase expires that many days after the payment.
5. **Customer portal.** `/c/{token}` shows the credit balance with its lots (remaining of granted, expiry) and a *Buy credits* list of the active packs. *Buy* is `POST /c/{token}/credits/buy` (`pack_id`, optional `quantity`): billing makes the credit-pack link and its invoice and the customer lands on the pay page. A second press while the first purchase is unpaid continues with the same invoice (no second number). Six languages in the gateway text maps.
6. **Events.** `credits.purchased` (after the grant) and `credits.low` (a consume took the balance from at or above the tenant's threshold to below it). The threshold is a **tenant** setting, `GET`/`PUT /v1/credits/settings`; 0 or unset is off. See [webhooks](../16-webhooks.md).
7. **Console.** In the *Credits* tab: validity on a new pack, *Create purchase link* per pack (customer and quantity; the address is shown once), the low-balance alert, and the *Source* of every ledger row (a purchase is marked). Six languages.

**Out (non-goals):**

- Refunding or taking back credits when a paid purchase is credited or refunded; the operator withdraws them by hand (phase 35's limit stands). A refund or void *before* payment grants nothing, because only `PAID` grants.
- A per-pack low-balance threshold: a balance is not tied to one pack, so the threshold is per tenant. Expiry does not announce `credits.low` either (only a consume does).
- Subscriptions or auto top-up.

## Decisions

- **A new link kind, not a flag.** `payment_links.kind = 'credit_pack'` with `pack_id`, `quantity`, `credits`, `validity_days` columns, constrained together by `payment_link_shape`. It behaves like an ad-hoc link (a factory: *Continue* makes the invoice and hands out a child invoice link) and the code asks `factory(kind)` rather than `kind == "adhoc"`.
- **The price is read where the link is made.** `fillFromPack` reads the pack in the creating transaction (`GetCreditPack`, tenant in the predicate), refuses a withdrawn pack, and stores the total. Raising the pack's price later does not change an existing link, and the client has no field to lower it.
- **One grant per payment.** The unique `(tenant, source, source_ref)` on lots (phase 35) with `source_ref` = the invoice id. `PurchasePaid` announces `credits.purchased` only when `GrantPurchase` says it added the lot, and the event id is derived from `(tenant, type, invoice)`, so retries repeat nothing.
- **Money logic stays in billing.** The hook is in billing-svc's saga (`creditsPurchased`), next to the `payment_link.paid` event. The gateway only renders and posts.
- **The portal buys for its own customer.** `portal_links` now remembers the customer's external id (the invoice is made out by it); a portal link made before this phase cannot buy and answers `billing.credits.portal_cannot_buy` until a new one is made. The link a portal press creates has the idempotency key `portal:{uuid}` and lives at most 30 days or until the portal link ends; `FindOpenPortalPackLink` finds an unpaid one for the same customer, pack and quantity.
- **Events from a consume.** A consume runs as `billing_app`; it writes `credits.low` into `tenant_event_outbox` in its own transaction (`billing_app` gets `INSERT` on the outbox; RLS still binds the tenant). The helper lives in `internal/tenantevents` so the saga and the credits module can both use it.
- **`credits.low` is once per crossing.** It fires when `before >= threshold > after`. A replayed consume returns before that point, and the event id is derived from the customer and the consume's idempotency key.

## Contracts

- Proto (additive): `CreatePaymentLinkRequest.pack_id/quantity`, `PaymentLink.pack_id/quantity/credits`, `CreditPack.validity_days`, `CreateCreditPackRequest.validity_days`, `CreditLedgerEntry.source`, `ConsumeCreditsRequest.customer_external_id`, `HostedAdhoc.pack`, `GetPortalResponse.credit_*`, `HostedInvoiceService.StartPortalPurchase`, `BillingService.GetCreditSettings/SetCreditSettings`.
- Gateway: `GET|PUT /v1/credits/settings`, `POST /c/{token}/credits/buy`; `POST /v1/payment-links` takes `kind: credit_pack`.
- Error keys: `billing.credits.pack_inactive`, `bad_quantity`, `bad_validity`, `bad_threshold`, `portal_cannot_buy`; `billing.payment_link.pack_required`, `customer_required`.
- Migration `00022_credit_purchase.sql`.

## Tasks

- [x] Plan (this file)
- [x] Migration, sqlc queries, proto
- [x] billing-svc: link kind, pack validity, grant on paid, portal purchase, settings, `credits.low`
- [x] plans-svc: the two event types in the catalogue
- [x] gateway: purchase page, portal credits and buy, settings routes, six languages, OpenAPI
- [x] console: validity, purchase link, low-balance alert, ledger source, six languages, mocked-gateway spec
- [x] tests: unit, gateway, billing integration (purchase, void before pay, portal, low), Playwright mocked
- [x] docs: webhooks, billing-svc, error keys, ROADMAP
- [ ] PR `feat/credit-purchase`

## Exit criteria

A sandbox tenant creates a pack with a 30-day validity, makes a purchase link for a customer (quantity 2), the customer opens it, presses *Continue* and *Pay*, the invoice is paid, and the customer has one lot of twice the pack's credits expiring in 30 days; the ledger shows the purchase; the tenant's endpoint got `credits.purchased` once; a duplicate confirmation grants nothing more; a link revoked before payment grants nothing. The customer's portal shows the balance and lots and starts the same purchase. A consume that crosses the tenant's threshold sends `credits.low` once.

## Not verified here

- The provider-hosted checkouts (Paddle, iyzico) were not run against sandboxes for credit purchases; they end in the same `SettleInvoicePayment` as any hosted payment, which the phase-34 tests cover with the mock.
