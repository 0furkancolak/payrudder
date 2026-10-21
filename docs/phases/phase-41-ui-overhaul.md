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

## Not done

- Operator-only pages (providers, e-invoice, seller address, reconciliation) stay hidden from a person: they need session-path routes with their own authorisation design.
- The dashboards are in the next pull request.
