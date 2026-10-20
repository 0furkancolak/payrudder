# Phase 21 — Paddle catalogue and the subscription API

## Goal

Quotify creates and keeps in sync what Paddle needs to sell a plan, and drives the Paddle subscriptions it mirrors, so an operator never has to open Paddle's dashboard for them. See [15-billing-lifecycle.md](../15-billing-lifecycle.md).

## Scope

**In:** provider-neutral optional interfaces in `pkg/payments` (`Catalogue`, `CustomerAPI`, `SubscriptionAPI`, `DiscountAPI`, `DocumentLinker`); their Paddle implementation (`pkg/payments/paddle/api.go`);
payments-worker `provider_objects` (what Quotify created at a provider) and the operator endpoints under `/admin/tenants/{tenant}/providers/{provider}/…`, forwarded by the gateway as `/admin/v1/tenants/{tenant_id}/providers/{provider}/…`;
plans-svc following a plan or interval change the provider made (`SyncProviderSubscription` carries `plan_version_id` and `interval`); the Paddle webhook naming the prices a subscription is billed for; console: a catalogue card on the Providers tab and a "Run by {provider}" panel on a provider-run subscription; six languages.

**Out of scope:** a Paddle sandbox run (no credentials; every path or field the reference does not settle is marked UNVERIFIED in the code); Paddle's reports, Retain and notification-destination management; Quotify coupons (phase 22) apart from creating a Paddle discount; a change that waits for the end of the period (Paddle cannot schedule an item change).

## What is exposed

| Operation | Endpoint (under `/admin/v1/tenants/{tenant_id}/providers/{provider}`) | Paddle call |
|---|---|---|
| Sync a plan version | `POST /catalogue/sync` `{plan_code, plan_version}` | `POST /products` once per plan, `POST /prices` per interval and currency |
| List what was created | `GET /catalogue` | none (Quotify's `provider_objects`) |
| Ensure a customer | `POST /customers/{external_id}` | `GET/POST /customers`, `/addresses`, `/businesses` |
| Portal link | `POST /customers/{external_id}/portal` | `POST /customers/{id}/portal-sessions` |
| Preview a change | `POST /subscriptions/{id}/change-preview` | `PATCH /subscriptions/{id}/preview` |
| Change plan or interval | `POST /subscriptions/{id}/change` | `PATCH /subscriptions/{id}` |
| Pause / resume | `POST /subscriptions/{id}/pause`, `/resume` | `POST …/pause`, `…/resume` |
| Update payment method | `POST /subscriptions/{id}/payment-method-link` | `GET …/update-payment-method-transaction` |
| Discount | `POST /discounts` | `POST /discounts` |
| Invoice document | `GET /transactions/{ref}/invoice` | `GET /transactions/{id}/invoice` |

Cancellation and refunds already existed (phases 15–16). An operation is offered only when the adapter implements the interface; iyzico, which Quotify runs itself, answers `501` and uses the tenant API.

## Decisions made while building

- **Prices are named by plan version, interval and currency** (`{plan version id}|{interval}|{CURRENCY}`) and stored in `provider_objects`; the first mapping of a key wins, so concurrent syncs agree. A plan version is immutable, so a price never needs editing: a new version is new prices. A price in a currency Paddle cannot bill (Paddle's list is declared in `Caps().Currencies`) is *reported as skipped*, not an error.
- **Partial progress is kept.** If creating the third price fails, the product and the first two are remembered, and the retry creates only the rest.
- **Quotify mirrors; Paddle bills.** A change is applied at Paddle and Quotify's subscription follows at once (the call tells plans-svc) and again from Paddle's `subscription.updated` (idempotent). Following a plan or interval change switches the subscription to its successor in the chain with no money moved (Paddle billed it) and hands over the provider id (`ReleaseProviderLink`).
- **Events name the live subscription.** After a switch, Paddle's events still carry the id Quotify first linked; plans-svc resolves them along the `changed_from_id` chain to the subscription that holds the provider id. An unrelated subscription that names a taken provider id is still refused.
- **Timing and proration map onto Paddle's `proration_billing_mode`:** immediately+prorate → `prorated_immediately`, immediately+full → `full_immediately`, immediately+none → `full_next_billing_period`. **A change for the end of the period is refused** with a reason: Paddle changes items at once, and approximating would give the customer the new plan early while the caller believed otherwise.
- **A payment that fails prevents the change** (`on_payment_failure: prevent_change`).
- **Resume starts a new billing period and charges at once** (Paddle's default, `start_new_billing_period`); the console says so.

## UNVERIFIED (no sandbox run)

Portal-session path and response (`urls.general.overview`); address and business bodies and listing; the inner shape of `update_summary` and `recurring_transaction_details` in a preview; the update-payment-method transaction response; the invoice link location; the exact effect of `full_next_billing_period` as "no money now". Each is read defensively (absent fields read as zero) and covered by a fixture.

## Tasks

- [x] `pkg/payments`: the optional interfaces
- [x] `pkg/payments/paddle/api.go` and fixture tests for every call
- [x] payments-worker: `provider_objects`, endpoints, the webhook following prices; integration tests
- [x] plans-svc: follow a provider's plan and interval change; resolve events along the chain
- [x] Gateway routes, OpenAPI, console, six languages, mocked e2e
- [ ] Run each call against the Paddle sandbox
