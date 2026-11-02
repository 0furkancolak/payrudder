# 02 — Console interaction contract: filters, sort, bulk actions

## The request
List 2: (6) filters apply when an **Apply** button is pressed, not on every click; (7) **one select** orders a list by column and direction, "wherever it is needed"; (12) **bulk actions**.

## Where we are
- Filters: `FilterSelect` holds a draft and applies on **Apply** (#249); every chip that uses it (32 places) behaves so.
- Sort: `SortSelect` + `useTableState.sorts` (#250), wired into customers, invoices, subscriptions, payments, expenses, tenants and the two support lists; **33 of 33 lists built with `useTableState` have one since the statuses below.**
- Bulk: ticks, `BulkBar`, `runBulk` (#251) in the operator support inbox (status/priority/assignee), the customer list (CSV export, block, unblock) and the operator tenant list (suspend, reinstate); **on ten lists since the statuses below** (also API keys, webhook endpoints, subscriptions, payment links, coupons, dead-letter queues and partners).

## Gaps (what was done too fast)
- **Filters:** only `FilterSelect` was changed. Other things that filter a list were not looked at: the date-range picker, the search box (immediate by design?), the operator support inbox's own filter selects and "late" checkbox (they apply on change), the `?…` URL state, server-paged lists that filter on the server, the Combobox/search-select used as a filter. No inventory was made. "Apply" is not discoverable on a phone (popover width), and there is no "applied filters" summary.
- ~~**Sort:** eight lists of roughly seventeen; the sort is not in the URL.~~ **33 of 33 lists built with `useTableState` have a `SortSelect`**, and the choice is in the address (`?sort=`) for every list that keeps its state there. Server-paged lists (payment links, app invoices, promotion codes, redemptions, organisations, the affiliate and partner tabs) have none, by the rule decided in `docs/13-admin-ui.md`; direction labels exist for time, amount, name, count and expiry.
- ~~**Bulk:** three lists; no select-all across pages, no progress for long runs, no keyboard support, no `aria-live`.~~ Bulk is on **ten lists** (customers, operator tenants, the support inbox, API keys, webhook endpoints, subscriptions, payment links, coupons, dead-letter queues, partners), with "select all N results", a bar that is a landmark with a polite live region and Escape, and a progress panel for a run of more than 20 rows. **Still not there:** undo, a batch call (a bulk action is one request per row, on purpose: see the last status), bulk on invoices, packages, meters and affiliates (the actions were never agreed, or the API has none).
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
- **Sort, step 2:** apps, meters, webhook endpoints, API keys, credit notes and packages gained it (14 lists now). Still without: tenant payments ledger, payment issues, members (no time column), usage tabs, deliveries, dead letters, coupons, credit packs/lots/ledger, reports, providers.
- **Sort in the URL:** done in `useTableState` (`?sort=`) for every list that has `url` and `sorts`.
- **Bulk:** support inbox, customers, tenants, and (step 3) API keys (revoke) and webhook endpoints (pause, resume, delete) through the shared `useBulk` (`shared/lib/bulk.ts`: one question when it cannot be undone, "n done, m not changed" with the names, failed rows stay ticked). Candidates, by list: invoices (download, remind), subscriptions (cancel at period end, resume), payment links (revoke), keys (revoke), members (remove), webhooks (pause, resume, delete), dead letters (redrive, discard), packages (archive), coupons and codes (deactivate), meters (archive), affiliates (approve, suspend, pay).

## Review addendum (2026-11-01)
- **Status:** the items under "Open work with no decision needed" below are done in the status section at the end of this file.
- **Counts are stale:** 63 `FilterSelect` in 35 files; bulk exists on five lists (customers, tenants, support inbox, `KeysPanel`, `WebhooksPage`); sort on 14 lists.
- **Decided, owner to confirm:** the server-paged sort question is settled in `docs/13-admin-ui.md` (section on sorting); do not reopen it.
- **Open work with no decision needed:** `aria-live` and keyboard support in `BulkBar.vue`; select-all-matching in `shared/lib/bulk.ts`; applied-filter chips and the 360px popover; client sort on the remaining client-paged lists; new strings in all six locales, Arabic RTL checked in the mock e2e.

## Status (2026-11-02, branch `feat/console-lists-peek`)

Done in this change, each with a test that fails without it (the contract is in `docs/13-admin-ui.md`, "Lists"):

| Item | What | Tests |
|---|---|---|
| Bulk bar for a screen reader and a keyboard | `BulkBar`: an always-present polite live region ("3 rows selected…", "Selection cleared"), Escape clears and returns the focus to the header's tick, nothing happens on an Escape that belongs to a sheet, popover or text box, Tab goes through the actions in order. | `e2e-mock/bulk-lists.spec.ts` (two tests) |
| Select all N results | `useTableState.selectAll` / `canSelectAll` / `allMatching`; the bar offers it when the page is ticked and more rows match; a narrower filter narrows the selection. Customers and tenants (fetched `PAGE.whole` at a time) fetch the other server pages by `page_token` first (`restOfPages`, `useRestOfCustomers`, `useRestOfTenants`). Any bulk action on more rows than a page holds is confirmed with the count (`bulkQuestion`). Customers, tenants and the support inbox moved from their own copies of the bulk code to `useBulk`; `useBulk` now takes the list's `useTableState` instead of three lambdas. | `bulk.test.ts`, `useTableState.test.ts`, `bulk-lists.spec.ts` (four tests: all matching with the count in the question, a filter that narrows it, the rest of the server pages, a page that cannot be loaded) |
| Sort on the remaining client-paged lists | 19 more tables (see the list below), 14 before: every list built with `useTableState` now has a `SortSelect`, 33 of 33. | `sort-lists.spec.ts` (credit packs, coupons, the tenant payments ledger, dead-letter queues, meters); the others are wired by the same four-line pattern and type-checked |
| Applied filters on a phone, RTL | `FilterSelect` reports itself to `DataToolbar` (`filter-select/applied.ts`), which lists the filters that are on as removable chips under the search when the filters are behind the drawer button; the popover fits 360 px inside the drawer and Apply is reachable. The RTL check found two real bugs, both fixed: layers (popover, menu, select) were `dir="ltr"` in Arabic (no `ConfigProvider`), and numeric Arabic dates were reordered (`022026/10/`) by a left-to-right isolate. | `e2e-mock/filters-chips.spec.ts` (4 tests), `format.test.ts` |

**Sort, step 3 (the lists).** Credit packs, a customer's credit lots, the credit ledger, e-invoice accounts, coupons, members, dead-letter queues and messages, the tenant payments ledger and its reconciliation issues, webhook deliveries, providers' accounts and the subscriptions they run, the provider catalogue, the revenue, subscription and dunning reports, the usage customers and meters tabs. Choices per list are in the `sorts` of each file; new labels: "Count, high to low / low to high", "Expires soonest" (six languages). The lists whose state is not in the address (`url`) keep the order in the page: moving each of them to `url` is a separate step because it also moves their filters into the address.

**Lists with no sort, and why** (also in `docs/13-admin-ui.md`): server-paged lists (payment links, app invoices, promotion codes, redemptions, organisations, every affiliate and partner tab) by the rule already decided; the detail tables inside one record's sheet; the all-apps view (no search, filters or paging yet).

**Defaults chosen, not agreed** (the owner said not to ask):
- "Select all N results" ticks the filtered set in the browser; for the two lists that are fetched in pages it first fetches the rest, without a cap on rows (a repeated token or 500 pages stops with an error). No new endpoint, no `select_all` flag on the API.
- A bulk action on more rows than one page holds is always confirmed, with the count, even when it could be undone. The threshold is the page size the person chose.
- The applied-filter chips exist where the filters are hidden (a phone). On a wide screen the filter chips themselves are the applied filters, as before. The date range is not a chip.
- Escape clears the selection only from inside the table's card; it is not a page-wide shortcut.

**Not done or not verified**
- Bulk on more lists (invoices, subscriptions, payment links, packages, dead letters, coupons, meters, affiliates): the actions were never agreed. The shared `useBulk` is what they would use.
- A batch endpoint: one request and one list refetch per row is still the cost of a bulk action (a 340-customer block makes 340 of each).
- No progress panel and no undo; the note after the run is the report.
- Screen-reader behaviour (the live region and the bar landmark are asserted in the DOM, not heard), the manual check at 1440, 768 and 360 px in English and Arabic with screenshots on the PR, and every list's own apply/clear and sort test (five lists have one, the rest are covered by the shared contract and the type check).

## Status (2026-11-02, branch `feat/console-bulk-more`)

The open console work of this plan, all in `web/admin` (no Go code, no API change). Details of the contract are in `docs/13-admin-ui.md`, "Lists".

| Item | What | Tests |
|---|---|---|
| Bulk on five more lists | Subscriptions (cancel at the period end with a question that counts them, keep = withdraw a scheduled cancellation, resume), payment links (revoke; the question says unpaid invoices made from them are voided), coupons (deactivate), dead-letter queues (redrive the ones that hold messages; the question counts queues and messages) and partners (approve, suspend with a question, reinstate). Each goes through `useBulk`/`runBulk`/`BulkBar`/`askConfirm`, acts only on the ticked rows the action fits (the question and the note count those), is shown only to somebody who may write (`useCanWrite`), reports "n done, m not changed" with the names and leaves the refused rows ticked. | `bulk-more.spec.ts` (16: per list the question with its count, one request per row, the refused row named and still ticked, a refused question sends nothing, a viewer sees no ticks; approve/reinstate and keep/resume at once), `subscriptions/lib.test.ts` (`bulkEligibility`) |
| Ticks of a server-paged list | `shared/lib/selection.ts`: `useSelection` (the ticks `useTableState` had, now one implementation) and `useServerPageSelection` for a list the server pages (payment links, partners). | `selection.test.ts`, the existing `useTableState` tests |
| State in the address | 20 tables that kept their state in the page now have `url` (with a prefix of their own where a page holds several): credit packs, a customer's credit lots and ledger, coupons, dead-letter queues, the review queue and the settlement, packages, the provider accounts, runs and catalogue, the three reports, the two usage tabs, the integration accounts, webhook deliveries and the two support lists. | `list-contract.spec.ts` (the choice is in the address and survives a reload) |
| Apply, clear and sort per list | One data-driven spec: 29 lists, each with its sort (the choice orders the rows, is written as `?<prefix>sort=`, survives a reload and is taken off by "As received") and its filter (a draft until Apply, the list follows after Apply, a reload keeps it, the cross of the chip clears it). Customers and the tenant's payment ledger keep the tests they had. | `list-contract.spec.ts` (54 tests: 28 sorts, 26 filters) |

**Bulk actions left out, and why** (written so they are not asked for again without the missing piece):
- *Packages and meters (archive):* the API has no archive or delete for a plan or a meter (a version is immutable); there is nothing to call. A bulk action needs the row action first.
- *Invoices (void, resend, download):* the console has no row action for void (the operator API `adminVoidInvoice` exists, refunds a paid invoice first and has no screen), the API has no resend, and "download selected" has no safe form: one tab per PDF runs into a browser's one-new-tab-per-click limit (not tried against a real stack) and the console's Content-Security-Policy (`connect-src 'self'`) does not let it fetch the files from the storage to save them itself. Both need a decision (a void screen with its refund wording) or a batch endpoint (a zip of PDFs), neither of which was marked.
- *Discard (dead letters):* the API only redrives a queue; discarding is a replay job's call, not a queue's.
- *Promotion codes, redemptions, affiliates' commissions and payouts:* server-paged with no agreed action.

**Defaults chosen, not agreed:**
- Cancelling a subscription at the period end asks first even though "keep" undoes it, because it ends revenue and the customer is told; "keep", "resume" and "approve" run at once (they undo or are harmless) unless more rows than one page are ticked.
- A list the server pages ticks the page it holds only: no "select all N results" there (the rest is not loaded, and a second fetch of unknown size is a decision for a list that has a batch call).
- Prefixes: `<table>-` for a new one (`packs-`, `lots-`, ...), the older `f_` and bare keys stay as they were, so no address that exists changes.

**Inventory of tests per list** (every `useTableState` table; 33 tables):
covered by `list-contract.spec.ts`: accounting expenses, withdrawals, credit packs, credit lots, credit ledger, e-invoice accounts, integration accounts, invoices, meters, packages, fee rules, review queue, settlement, coupons, API keys, members, the person's payments, provider accounts, provider runs, the three reports, subscriptions, the operator's tenants, the two usage tabs, webhook endpoints, dead-letter queues, the person's support tickets; by their own specs: customers (`customers.spec.ts`) and the tenant's payment ledger (`sort-lists.spec.ts`). **Not covered:** apps (needs an account's app list), the credit notes of an invoice, the messages of a dead-letter queue and a webhook's deliveries (all three live inside a sheet), the provider catalogue (needs a provider and an environment chosen first) and the operator's support inbox (its filters are the server's; `support.spec.ts` has its filter and sort tests).

**Not verified:** no real stack (a bulk run against many real rows), no screen reader (the bar's live region was asserted in the DOM), no phone. Screenshots of the five changed lists at 1440, 768 and 360 px and in Arabic at 1440 and 360 px were looked at (in `shots` of the PR, not committed): the bar wraps under the search on a phone and mirrors in Arabic; nothing was wider than the screen. "Select all N results" for the server-paged lists, a progress panel and an undo are still not there.

## Status (2026-11-02, branch `feat/bulk-progress-3`): progress panel, the six lists, two bugs the lists found

| Item | What | Tests |
|---|---|---|
| Progress of a long run | `useBulk` passes `progress` ({done, failed, total}) to `BulkBar` for a run on more than 20 rows (`BULK_PROGRESS_FROM`): a bar and "8 of 23 done · 1 not changed" while it runs, a `progressbar` role with `aria-valuenow` (not a live region: a row-by-row announcement would drown the reader; the page's note says the result at the end). Every page that binds `bulk.bar` gets it, eleven pages, none changed. Three strings in six languages (`common.bulk.progress*`). | `bulk.test.ts` (`onStep`), `bulk-lists.spec.ts` (a 23-row run held at the tenth request: the text, the bar's values, gone at the end, the refused row still ticked; a run of 10 shows no panel) |
| The six lists plan 02 listed as uncovered | `list-contract.spec.ts` now covers an account's apps, the credit notes, a webhook's deliveries, a dead-letter queue's messages, the provider's catalogue and the operator's support inbox: 11 more tests (65 in the file, 35 lists). Two of them keep their state out of the address and say so in the spec: the catalogue (the provider and package it shows are the card's own state, so a reload shows nothing) and the support inbox (its filters are the page's state sent to the server; only the sort is in the address). | `list-contract.spec.ts` |
| **Bug found: a dead-letter queue's sheet closed as it opened.** Opening a queue changes the query key; until the answer came the list of queues was empty, the row the sheet belongs to was gone and the sheet closed. | `useDeadLetters` keeps the previous answer (`keepPreviousData`) and the "no messages" line waits for the real one. | `list-contract.spec.ts` (messages of a dead-letter queue: red before) |
| **Bug found: a webhook's deliveries lost their filter on a reload.** The sheet clears its deliveries table when it opens, which also wiped the filter the address had just brought back. | A first opening (a reload, a shared link) keeps what the address says; another endpoint, or closing, starts clean. | `list-contract.spec.ts` (webhook deliveries: red before) |

**No batch endpoint (default, not agreed).** The brief allowed `POST /v1/.../batch` for customers block and unblock if they are idempotent, and they are per customer (`Block` keeps a block that is in force and records nothing new; `Unblock` returns a customer who was never blocked as it is). It is not built: the definition of done for an API change (proto or gateway route, `openapi`, four generated SDKs, the TypeScript types, docs-site, MCP tool list, error keys) is a change of its own, other agents edit the same generated files in parallel, and the cost it would remove is requests, not correctness (a 340-customer block makes 340 requests and 340 list refetches; the progress panel makes that visible). Revisit when a seller blocks thousands at once; a gateway loop over the existing calls with a per-row result (`{external_id, ok, error}`), each customer in its own transaction as now, would keep the partial-failure behaviour the console already has.

**Still open:** the support inbox's filters and the catalogue's choices are not in the address; the screens were not looked at on a phone or in Arabic with the new panel (the bar at 1440 px in English was); no screen reader.

## Status (2026-11-02, branch `fix/console-review-3`): the final console review, controls around the lists

The last review of the console (every page read against `docs/13-admin-ui.md`) found the contract kept on the lists and broken around them. Fixed, each with a mock e2e:

| Finding | Fix | Held by |
|---|---|---|
| A viewer was offered controls the server refuses: new customer and bulk block, new webhook endpoint and its bulk pause, resume and delete, scan receipts and add expense, new credit pack, the mail switches, the new subscription and payment link buttons. | `useCanWrite` in each (the "new" button, the ticks and the bar, the row actions and the sheet buttons that write; the mail switches stay and are off). The customer's sheet is read-only for a viewer as it is in a peek. | `viewer-gating.spec.ts` (one test per page and role) |
| One-click destructive actions: deactivating a coupon or a promotion code, cancelling a payout, regenerating a customer's payment page (it revokes the links already sent), removing a customer's quota override, cancelling an invitation, lowering a role, closing a ticket. | `askConfirm` with the words of the bulk equivalent where there is one; closing tickets in the support inbox's bulk bar asks with the count. A refused role change draws the select again with the member's real role. | `destructive-confirm.spec.ts` |
| A failed read looked like an empty list: the related invoices, subscriptions, payments and credit notes of a sheet, a customer's block history, and the lists and cards that showed an `ErrorText` line. | `ErrorState` with "Try again" (`RelatedSection` takes `error`). | `failed-queries.spec.ts` |

**Defaults (not agreed):** a viewer loses the selected-customers CSV export (the contract gives a viewer no ticks; the export is a button of the bulk bar); coupons keep their disabled "new" button for a viewer (a choice of that page that the finding did not name).

**Still open:** the subscription's manage sheet and the credits page's other cards (grant, low-balance alert, purchase links) still show write buttons to a viewer. Not looked at on a phone.

## Status (2026-11-02), branch `feat/leftovers-4-dx-console`: the list nobody had covered, and the viewer's last write controls

- **The inventory of tests per list above is out of date, and its "Not covered" list is closed.** Reading `useTableState` against `list-contract.spec.ts` (33 tables in 33 files) found one list the contract had never been run on: the **disputes** tab of the payments page, added after the last inventory (it had a spec of its own for outcome, search and sort, but not for the address or the chip's cross). It is now a case of the contract (`disputes (person)`: the sort in `?disputes-sort=` and surviving a reload, the outcome a draft until Apply, kept by a reload, taken off by the chip's cross; 2 tests, 68 in the file). Every list built with `useTableState` is in the contract now; the six the older inventory called uncovered (an account's apps, an invoice's credit notes, a webhook's deliveries, a dead-letter queue's messages, the provider catalogue, the operator's support inbox) were covered by the `feat/bulk-progress-3` status and stay so.
- **The "Still open" line of the review part is closed:** the subscription's manage sheet and the credits page's other cards no longer show write buttons to a viewer, and neither do the Connect page, the sandbox's simulate on an invoice, a purchase-link sheet named in an address or the delete-card button of a customer's sheet (that one was a real bug: it followed the sheet's `readonly` prop, which a viewer's sheet does not set). Details in `docs/13-admin-ui.md` ("A viewer reads") and `e2e-mock/viewer-gating.spec.ts`: seven new tests failed without the change (broken once on purpose), the owner's twin of each passed, 28 in the file.

**Still open:** the support inbox's filters and the catalogue's choices are not in the address; a list the server pages still has no select-all; no screen reader, no phone with the progress panel (a phone and a tablet were looked at for the pages in `e2e-mock/phone-tablet.spec.ts`, see plan 04).
