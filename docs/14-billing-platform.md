# Billing platform: subscriptions, payment providers, routing, reporting

Status: phases 13 to 18 are built (see the notes below); phases 19–22 are proposed in [15-billing-lifecycle.md](15-billing-lifecycle.md). Phases 13–18 in the [roadmap](phases/ROADMAP.md) build it. Decisions are in [ADR 0016](adr/0016-provider-capabilities-and-routing.md).

## Why

Quotify meters usage and turns a closed period into an invoice and a payment. That is one slice of billing. A SaaS company that bills its own customers also needs
packages that renew, trials, plan changes, payment history, refunds, and more than one payment provider because no single provider covers every country and currency.
Two providers are the first targets: **iyzico** (Turkey, TRY) and **Paddle** (international, merchant of record).

Quotify stays the billing engine of the SaaS company (the tenant). It is not a merchant of record. Card data never touches Quotify.

## Goals

- Subscriptions with packages: fixed fee, usage-based fee, or both on one invoice. Trial, active, past due, paused, cancelled. Upgrade, downgrade, proration.
- Each payment provider is used for what it can do. The rest is done by Quotify.
- Routing: the provider is chosen per customer from country and currency by rules the tenant edits.
- Migration of a customer's subscription from one provider to another without losing history or double charging.
- One ledger of every charge, refund, dispute and payout across providers, and reports by provider, country and currency.
- A clean console UI built from the shared shadcn-vue kit (`web/admin/src/shared/ui`).

## Non-goals

- Becoming a merchant of record, calculating or remitting tax. Paddle does that for its customers; for iyzico the tenant's invoice tax setup is the tenant's own.
- Storing card numbers or running PCI-scope screens. Providers host the payment form.
- Foreign-exchange conversion of money. Reports show each currency by itself, and a reporting currency only when the tenant supplies the rate (see Reporting).
- A third provider in the first release. The adapter contract must make one easy to add.

## Anti-goals

- Money as a float, anywhere. Minor units and `pkg/money` throughout (ADR 0010).
- Two engines both retrying the same failed payment. For every subscription exactly one party owns dunning.
- A provider switch that can charge a customer twice.

## Domain model

New entities, all tenant-scoped under RLS like the existing tables:

| Entity | Meaning |
|---|---|
| `customer` | A tenant's end customer: name, email, country, billing currency, tax identifiers. |
| `provider_account` | A tenant's credentials for a provider (references a secret, never stores it in plain text) and the provider's mode (test/live). |
| `provider_customer` | The customer's id at a provider, one row per provider the customer has used. |
| `package` | A sellable plan: fixed price per interval, usage meters with included quantity, trial days. Prices are per currency. |
| `subscription` | A customer on a package version: state, current period, owning provider, pending change. |
| `payment` | One attempt or event at a provider: charge, refund, dispute, payout. Append-only ledger row with provider reference. |
| `routing_rule` | Ordered rules: match on country and currency, result is a provider, first match wins, one default. |

The existing `invoice` and billing saga stay. An invoice gains `customer_id`, `subscription_id` and `provider`.

## Provider capabilities

A provider adapter declares what it does for us. Quotify does the remainder.

| Capability | iyzico (planned) | Paddle (planned) | Mock PSP (today) |
|---|---|---|---|
| Recurring schedule and renewal | Quotify (provider has subscription products, but Quotify drives it from its own schedule so usage and fixed fees land on one invoice) | Provider | Quotify |
| Dunning (retry failed payment) | Quotify | Provider | Quotify |
| Tax calculation | Tenant | Provider | none |
| Invoice document | Quotify | Provider | Quotify |
| Hosted payment form | Provider | Provider | none |
| Refund API | Provider | Provider | Provider |
| Usage-based charges | Quotify bills a closed-period invoice | Quotify reports usage, Paddle charges | Quotify |

These rows are the starting position from public provider documentation and must be confirmed against each provider's sandbox in phases 14 and 15.
When the real behaviour differs the table and the adapter change, not the contract.

The contract is a Go interface in `pkg/payments`, extended from today's `PaymentProvider`: `Capabilities()`, `Charge`, `Refund`, `Status`, plus `CreateCustomer`,
`CreateCheckout`, `CancelSubscription`, `ReportUsage` for providers that own part of the lifecycle. A call outside a provider's capabilities returns `ErrUnsupported`,
and the capability flags let the saga decide before it asks.

**Ownership rule:** every subscription has one `owner` (`quotify` or a provider) for renewal and dunning. The saga skips steps the provider owns and waits for the provider's webhook instead.

## Webhooks

Each provider gets a verified endpoint on the gateway (`/webhooks/<provider>`). The body is checked against the provider's signature, written to an inbox table, acknowledged,
and processed asynchronously by the worker. Processing is idempotent on the provider's event id. Out-of-order events are reconciled against the provider's current state.
A periodic reconciliation job compares our ledger with the provider's list API and raises a discrepancy report instead of fixing silently.

## Routing

A customer's provider is chosen when the subscription is created: the first `routing_rule` that matches the customer's country and billing currency, else the default.
The choice is stored on the subscription, so editing rules never moves existing customers. A rule that points at a provider unable to charge that currency is rejected on save.
The preview in the console shows "customer in X paying in Y goes to Z" before a rule is saved.

## Provider migration

Moving a subscription from provider A to B is a saga, like the billing saga, with these steps: freeze renewal, create the customer at B and collect a new payment method
(the customer completes B's hosted form, since cards do not move between providers by us), create the subscription at B aligned to the current period end, then cancel at A
after B's first successful charge. A failure before B charges leaves A unchanged. The step that cancels A runs only after B's charge is confirmed, so the worst case is an
overlap that the saga detects and refunds, never silent double billing. Every step writes ledger and audit rows.

## As built: phase 13

Differences from the design above, decided while building (details in [phase 13](phases/phase-13-subscriptions.md)):

- The design's `package` is the existing `plan` and `plan_version`; they gained a fixed fee and usage prices per currency.
- The `customer`, `subscription` tables exist already in `plans_db` and were extended instead of new ones being added.
- Billing periods are the tenant's calendar months, not anniversaries. A plan change splits the month's fixed fee by time on each plan;
  there is no stored proration amount.
- Yearly subscriptions are refused until the fixed fee can be invoiced once a year.
- Subscriptions in a currency other than the tenant's are not re-rated.

## As built: phase 14

Details in [phase 14](phases/phase-14-provider-contract-iyzico.md). In short: providers declare `Capabilities` and are built from a tenant's sealed
account settings; iyzico is the first adapter (stored-card charges, refunds, status, confirmation of webhooks) and Quotify keeps ownership of
renewal and dunning for it. Every charge and refund lands in an append-only ledger in `payments_db`; notifications arrive on the gateway's public
`/webhooks/{provider}`, are never believed, and are confirmed with the provider. The `Owner` of a subscription is derived from the capabilities but
not yet stored on it, since only Quotify-owned providers exist until Paddle (phase 15).

## As built: phase 15

Details in [phase 15](phases/phase-15-paddle.md). Paddle is the first provider that owns renewal and dunning: a subscription gets `provider`, `provider_ref` and `owner`
(plans-svc), Paddle's signed notifications link and follow it, its fixed fee stays off Quotify's invoice, and the usage total is charged on the customer's Paddle subscription as a
one-time charge that settles asynchronously. Dunning of that usage charge stays Quotify's saga; renewals are Paddle's.

## As built: phase 16

Details in [phase 16](phases/phase-16-routing-migration.md). Routing rules live in plans-svc and are checked against the provider catalogue; a subscription's provider is fixed at creation. A migration is
`started` while the customer subscribes at a provider that runs subscriptions, and the old subscription is untouched until the provider confirms; from a provider-run subscription to Quotify's the old provider is told to
stop first and the cutover is the end of the period it was paid for. The design's step list (freeze renewal, create at B, cancel at A after B's first charge) is realised as that confirmation-then-cutover rule.

## As built: phase 17

Details in [phase 17](phases/phase-17-reports.md). The reports are queries over the services' own data (invoices, subscriptions, the payment ledger), not a ClickHouse read model as designed: revenue and dunning in billing-svc, subscriptions and MRR in plans-svc, and a reconciliation of invoices against the
ledger in payments-worker. A currency is never added to another.

## As built: phase 18

Details in [phase 18](phases/phase-18-billing-console.md). The console's tenant page is a layout with seven tabs; the billing pages call the tenant API as the tenant (operator token plus `X-Quotify-Tenant`) and the operator routes
as the operator, are in six languages, and are tested against a mocked gateway.

## As built: phase 19

Details in [phase 19](phases/phase-19-yearly-advance.md) and [15-billing-lifecycle.md](15-billing-lifecycle.md). Yearly subscriptions exist; a plan version has a fixed fee per interval and currency; new subscriptions are invoiced in advance (a `FEE` invoice per period)
and keep usage in arrears on the monthly run. The "yearly subscriptions are refused" limit of phase 13 is gone.

## Currency

The old rule "one currency per tenant, immutable" moves down a level. The tenant keeps a **default currency**. A subscription and its invoices have one currency, fixed for the
life of the subscription. A customer may hold subscriptions in different currencies. Packages carry a price per currency. Money values stay minor units with the currency beside them.

## Reporting

From the ledger and invoices, by provider, country, currency and period: revenue billed, collected, refunded, disputed and outstanding. MRR and churn per currency,
a failed-payment and dunning view, and a reconciliation view of discrepancies. Currencies are never added together. If the tenant enters a rate table, a reporting-currency column
is shown labelled with the rate date. Reports are served from ClickHouse (analytics-svc) fed from the ledger events.

## Console

Features under `web/admin/src/features/`: `customers`, `packages`, `subscriptions`, `payments`, `providers` (accounts and routing rules with the preview), `reports`.
Built only from `shared/ui` components. Tables with filters, a subscription page with its timeline, and a migration wizard. All strings in the six languages.

## Delivery

Provider sandbox accounts need credentials from the tenant's side. Until they are present, each adapter is built against a recorded-fixture fake of the provider API
and the live sandbox test is skipped with a clear message; it is not reported as passing.

## Open points for the owner

- Confirm the currency rule above (tenant default, currency fixed per subscription) instead of separate tenants per currency.
- iyzico and Paddle sandbox credentials, and whether iyzico should use its own subscription product or Quotify's scheduler (table row 1 is our default).
- Which countries go to which provider by default.
