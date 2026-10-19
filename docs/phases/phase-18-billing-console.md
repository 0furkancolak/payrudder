# Phase 18 — Billing console

## Goal

An operator runs the billing platform from the console: customers, packages, subscriptions (and moving them between providers), payments, provider accounts and routing, and the reports. See
[13-admin-ui.md](../13-admin-ui.md) and [14-billing-platform.md](../14-billing-platform.md).

## Scope

**In:** the tenant page as a layout with seven tabs; the pages for customers, packages, subscriptions, payments, providers and reports on the shadcn-vue kit, in the six languages; an operator
"act as tenant" path on the gateway; subscription listing and a plan name on every subscription; tests against a mocked gateway.

**Out of scope:** metered prices in the package form (usage prices are set through the API), editing a published package, a tenant-facing portal, charts, paging beyond the first page of a list, exporting a report.

## Decisions made while building

- **The console uses the tenant API as the tenant.** The operator token plus `X-Quotify-Tenant` is accepted on tenant routes (the operator token alone is still refused there, a tenant's key cannot name another
  tenant). The billing pages therefore call the same endpoints, with the same validation and limits, as an integrator; there are no operator copies to keep in step. Provider accounts, the ledger,
  reconciliation and provider moves are operator routes and go as the operator.
- **Amounts are typed in major units and sent in minor units**, converted as text (`parseMajor`): an amount with more decimals than the currency has is refused before anything is sent.
- **A page is a form and a table, not a dialog.** Forms sit above the list, and a subscription's actions are a panel under it, which keeps everything reachable by label (and testable).
- **Tests run the built console against a mocked gateway** (`e2e-mock/`): no stack, so they run in the console's CI job and check what the console sends (headers, query, bodies) as well as what it shows.
  The real-stack tests in `e2e/` are unchanged.
- **Local actions are not offered on a provider-run subscription**, since the API refuses them; moving it to another provider is.
- **Provider settings are a JSON text area** pre-filled with the provider's template: the settings differ per provider and are sealed server-side, so the console does not model them.

## Tasks

- [x] Gateway: operator acts as a tenant; `GET /v1/subscriptions`; `plan_code` on a subscription
- [x] Tenant layout with tabs; customers, packages, subscriptions, payments, providers, reports pages
- [x] Six languages (a unit test keeps them complete)
- [x] Playwright tests against a mocked gateway, in CI
- [x] Docs

## Deliverables

- Every operation of phases 13 to 17 can be done and read in the console.
