# Phase 25 — Addresses and apps

## Scope

- **Apps.** `apps` between `accounts` and `tenants` (migration moves each account's two tenants under a default app). `CreateApp`, `ListApps`; API keys belong to an app environment; the console switches app and environment. Tenant ids stay the unit of isolation; the app id is shown beside it in the API and the console.
- **Addresses.** Customer billing address and tenant (seller) address: `line1`, `line2`, `city`, `region`, `postal_code`, `country`; validated (country ISO, lengths); in the invoice snapshot, the PDF, the hosted page and the tax-country lookup. API, console forms, six languages.

## Exit criteria

An account creates a second app with its own keys, customers and plans, isolated from the first (RLS test across apps); an invoice shows both addresses.
