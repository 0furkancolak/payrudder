# Phase 25 — Addresses and apps

## Scope

- **Apps.** `apps` between `accounts` and `tenants` (migration moves each account's two tenants under a default app). `CreateApp`, `ListApps`; API keys belong to an app environment; the console switches app and environment. Tenant ids stay the unit of isolation; the app id is shown beside it in the API and the console.
- **Addresses.** Customer billing address and tenant (seller) address: `line1`, `line2`, `city`, `region`, `postal_code`, `country`; validated (country ISO, lengths); in the invoice snapshot, the PDF, the hosted page and the tax-country lookup. API, console forms, six languages.

## Exit criteria

An account creates a second app with its own keys, customers and plans, isolated from the first (RLS test across apps); an invoice shows both addresses.

## As built

- **Apps:** table `apps` (migration 00013 gives every existing account a `default` app and moves its tenants under it); a tenant has an `app_id`, unique with its environment; `account_id` and `app_id` are immutable. `CreateAccount` makes the default app; operator routes `POST/GET /admin/v1/accounts/{id}/apps` make and list apps, each with its own sandbox and live tenant and its own currency, time zone and grace period. Keys, customers, plans, subscriptions and invoices were already per tenant, so they are per app environment without a change of their queries. Test: a customer of one app is not visible in another app of the same account.
- **Addresses:** one JSON object per customer and per tenant (migration 00014); `address` on customer create, `PUT /v1/customers/{external_id}/address`, and `address` in the operator's tenant settings. Validated (parts at most 120 characters, no line breaks, ISO country); the address country is the customer's country, so routing and tax read one value. The invoice snapshot freezes both addresses as printed lines (`pkg/address`), the PDF and the hosted page print them.
- **Console:** a billing-address fieldset when creating a customer, an address column with an editor in the customer list. An account's apps and the seller address have no console page yet (the operator API has them).
