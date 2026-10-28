# 02 — Console interaction contract: filters, sort, bulk actions

## The request
List 2: (6) filters apply when an **Apply** button is pressed, not on every click; (7) **one select** orders a list by column and direction, "wherever it is needed"; (12) **bulk actions**.

## Where we are
- Filters: `FilterSelect` holds a draft and applies on **Apply** (#249); every chip that uses it (32 places) behaves so.
- Sort: `SortSelect` + `useTableState.sorts` (#250), wired into customers, invoices, subscriptions, payments, expenses, tenants and the two support lists.
- Bulk: ticks, `BulkBar`, `runBulk` (#251) in the operator support inbox (status/priority/assignee), the customer list (CSV export, block, unblock) and the operator tenant list (suspend, reinstate).

## Gaps (what was done too fast)
- **Filters:** only `FilterSelect` was changed. Other things that filter a list were not looked at: the date-range picker, the search box (immediate by design?), the operator support inbox's own filter selects and "late" checkbox (they apply on change), the `?…` URL state, server-paged lists that filter on the server, the Combobox/search-select used as a filter. No inventory was made. "Apply" is not discoverable on a phone (popover width), and there is no "applied filters" summary.
- **Sort:** eight lists of roughly seventeen; the others (credit notes, packages, meters, apps, keys, members, webhooks, dead letters, e-invoices, payment links, affiliates…) have none. Sorting is client-side on the loaded rows; **server-paged lists** (invoices by token, payments limited to 200) sort only the page held, which is misleading. The sort is not in the URL, so it is lost on reload and cannot be shared. Direction labels exist for time, amount and name only.
- **Bulk:** three lists. No select-all across pages ("all 340 matching"), no progress for long runs, no undo, no per-row error detail beyond ids, and bulk acts one request at a time. Invoices, subscriptions, payment links, packages, webhooks have none. No keyboard support, no `aria-live` announcement of the result.
- No visual/accessibility review (focus order, screen reader names, RTL for Arabic).

## Research first
1. **Inventory** every list/table and every filter-like control in `web/admin/src` (grep `useTableState`, `FilterSelect`, `DateRangePicker`, `DataToolbar`, token-paged lists). Produce a table: list, client- or server-paged, filters it has, how they apply today, sort today, row actions, candidate bulk actions.
2. Read how server-paged lists page (`DataPagination` token mode) to decide what "sort" can mean there (server `order_by` vs. refusing sort).
3. Look at what Stripe, Paddle and Linear do for applied filters and bulk selection (select-all-matching, selection bar) and write down the rules chosen.

## Decisions needed
- Sort on server-paged lists: add `order_by` to those API endpoints (a contract change, SDKs), or sort only when all rows are loaded and say so?
- Which bulk actions are wanted on which lists (the owner's list says "bulk actions" without naming them).
- Whether the search box also waits for Apply (owner said filters; a search box that applies on typing is conventional).

## Work
1. Write the contract as a short doc (`docs/13-admin-ui.md` section): what applies immediately (sort, search), what waits for Apply (filters, date range), how selection works, how a bulk result is reported.
2. Filters: bring every filter-like control under the contract; add an "applied filters" row of removable chips; make the popover usable at 360 px; keep the URL as the source of truth.
3. Sort: add to every list that has more than one useful order; keep it in the URL (`?sort=`); decide server vs client per list (see decisions); tests for each list's choices.
4. Bulk: add select-all-matching, a progress and result panel, `aria-live`, keyboard; then the agreed actions list by list; for actions that can be one request (e.g. cancel many), add a batch endpoint only when the one-by-one cost is real.
5. Accessibility and RTL pass; mobile pass at 360 px.

## Done means
- The inventory table is complete and every row is either done or explicitly "not applicable" with a reason.
- Each list has an e2e test of apply/clear, sort, and (where it has bulk) select/act/partial-failure.
- Manual check at 1440, 768 and 360 px, in English and Arabic, with screenshots attached to the PR.

## Not verifiable here
Screen-reader behaviour (needs a person with the tools).

## Risks
Touching every list at once is a wide change: do it list by list in small PRs, never "one PR for all lists".

## Inventory (2026-10-29)

50 lists/tables were found in `web/admin/src`. Status of the interaction contract (`docs/13-admin-ui.md`, "Lists"):

- **Filters on Apply:** every list uses `FilterSelect` / `DateRangePicker` except the two that were fixed in the first contract PR (support inbox: status, priority, "only late"; accounting: "show voided"). Two text boxes of the support inbox (assignee, search) apply when left, as a search does. `saas/OrganizationsPage` has a search form only.
- **Sort:** `SortSelect` is on customers, invoices, subscriptions, expenses, tenants, product payments and the two support lists (8). Missing, client-side lists where it makes sense: tenant payments ledger, payment issues, credit notes, apps, keys, members, packages, meters, usage tabs, webhooks, deliveries, dead letters, coupons, credit packs/lots/ledger, reports, providers. **Server-paged lists get no client sort** (payment links, app invoices, promotion codes, redemptions, organisations, all affiliate and partner tabs) by the rule above.
- **Sort in the URL:** done in `useTableState` (`?sort=`) for every list that has `url` and `sorts`.
- **Bulk:** support inbox, customers, tenants. Candidates, by list: invoices (download, remind), subscriptions (cancel at period end, resume), payment links (revoke), keys (revoke), members (remove), webhooks (pause, resume, delete), dead letters (redrive, discard), packages (archive), coupons and codes (deactivate), meters (archive), affiliates (approve, suspend, pay).
