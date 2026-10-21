# Phase 41 — UI overhaul: the frame, privacy mode, the pickers and every page for a person

## Goal

The owner's feedback on the console: the sidebar must be open and follow the page, the layout must be responsive, buttons must look clickable, every relevant page must exist for a person, there must be a privacy mode, every currency and time zone must be choosable, an app may sell in several currencies ([phase 40](phase-40-multi-currency.md)), and the dashboard must be more advanced. It shipped as a backend pull request (phase 40), this one (the frame, B1) and the dashboards (B2).

## Scope (B1)

- [x] **Frame.** Sidebar open by default, foldable, remembered; drawer on phones and tablets with focus handling; breadcrumbs; app and environment switchers; skip link; right-to-left.
- [x] **Navigation follows the route.** Organisation, app, platform and tenant levels drawn from `nav.ts`; the active item and the section list change with the route.
- [x] **Every page for a person** under `/app/t/{tenant}`: customers, subscriptions, credits, packages, pricing and tax, invoices (list and detail with PDF and share link), payments, payment links, accounting, reports, webhooks, e-mail; under `/app/apps/{app}`: dashboard, API keys, app settings; members as a page of its own. Invoices and payments are the product's own pages from phase 39 (with `GET /v1/payments`); the operator gets an invoices page too.
- [x] **Privacy mode** through `formatMinor`, plus customer e-mail addresses; Alt+P; remembered; followed across tabs.
- [x] **Searchable currency and time zone fields**, all 153 currencies and every zone the browser knows.
- [x] **Pointer cursors, disabled cursors, 44 px touch targets.**
- [x] **Lazy routes and lazy locales** (first load 242 kB to 77 kB gzipped).
- [x] **Fixes from the real-browser report**: the accounting chart legend no longer overlaps the dates on a phone, form grids wait for 1280 px now that the sidebar takes the width, buttons wrap on a phone instead of scrolling the page.
- [x] **Tests.** Vitest (navigation, Intl helpers, masking), mocked Playwright (`shell.spec.ts`: sidebar open/contextual/folded/remembered, blocked storage, environment and app switchers, privacy mode, pickers, cursors, five widths, right to left), the existing specs follow the new frame.

## Decisions

- **Masking is in the text, not a blur.** A blurred number is still in the page (copy, screenshot of a blurred layer, accessibility tree). `formatMinor` returns `••••` with the currency, one fixed width so the mask does not tell the size.
- **One description of the navigation.** The sidebar, the drawer and the breadcrumbs are drawn from `buildNav`, so they cannot disagree and a test can check them.
- **The same pages for both audiences.** Instead of a second copy of each page for people, the console's pages ask the tenant API for a person (`useTenant` and the apps list read the product API) and the admin API for the operator; a refused `/admin` call never ends a person's session.
- **Breakpoint.** The sidebar needs 1024 px; below it a drawer. Page grids that used `lg:` columns use `xl:` so a card does not get 150 px.

## Scope (B2, dashboards)

- [x] **Gateway endpoints** `GET /app/v1/apps/{id}/dashboard` and `GET /admin/v1/tenants/{tenant_id}/dashboard`: a period (7d, 30d, 90d, ytd, custom) with the one before it, one block per currency, partial answers, a 15 second cache; error keys `gateway.dashboard.bad_period` and `.range_too_long` in six languages.
- [x] **Backend facts.** plans-svc: customer counts over two ranges, subscription report by plan (MRR per package). billing-svc: top customers per currency, open-invoice ageing, newest invoices; migration `00024`.
- [x] **UI.** App, organisation and operator-tenant dashboards: period bar, currency tabs, eight KPIs with change, revenue chart, payments donut, top customers and packages, ageing, usage against limits, upcoming charges, activity, health; empty, loading, error and degraded states; privacy mode, right to left, phone to desktop.
- [x] **Fixes from the real-browser report.** Revenue is net and says so (it agreed with nothing before); payments are counted per attempt with refunds apart; the sign-in error now says that five wrong passwords pause sign-in for 15 minutes; the customer portal and hosted pages style their links.
- [x] **Tests.** Gateway (period parsing, per-currency payments, subscriptions, renewals, usage, endpoint composition, cache, isolation, degraded), integration for billing-svc and plans-svc, vitest (period, delta, chart geometry), mocked Playwright `dashboard.spec.ts`.

## Not done

- The hosted payment page still shows no button for a few seconds after "Continue" (until the saga reaches awaiting payment); only a hint would help and it was left.
- Subscription figures are monthly (the report is), so MRR, churn and new subscribers compare this month with last month whatever period is chosen; renewals are worked out from the subscription's anchor and fee timing, not read from billing.
- Operator-only pages (providers, e-invoice, seller address, reconciliation) stay hidden from a person: they need session-path routes with their own authorisation design.
- The dashboards are the third pull request (below).
