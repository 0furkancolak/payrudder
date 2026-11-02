# 03 — Sheets, onboarding, tours, icons

## The request
List 1: (5) onboarding has needless forcing: steps must be skippable ("atla"); (6) the Payrudder welcome tour keeps appearing; (7) Usage and Meters need different icons; (9) on any scrolled page, opening a sheet scrolls the page behind it to the top (and back on close), and the multi-select inside a sheet does not scroll correctly; (13) some sheets navigate to another page instead of opening on top.

## Where we are
- #238: tour marked seen at start (`startTourOnce`), "Skip this step" in the old five-step wizard, Usage → `Activity` icon, sheet URL change uses `resetScroll: false`, Combobox scrolls only its list, invoice mentions and payment links open as sheets over the page (`PeekInvoice`).
- **#243 (another session) then rewrote onboarding into three screens and changed empty states / page headers.** The skip button and its test were part of the older wizard; the current state of 5, 6 and 13 after #243 has not been re-verified. ~~The mock e2e for onboarding fails on main.~~ It was red when this was written (plan 01 fixed the eleven failing mock tests in #246 and #259); `onboarding.spec.ts` passes in CI now.

## Gaps
- Skipping was added to the old wizard only; the new three-screen onboarding must be checked against "nothing forced".
- The welcome tour fix was verified by one e2e; the real cause (the seen list is kept on the server per person: what if the save fails, or a second device) was not reproduced against the real stack.
- Scroll fix was checked on one page in the mock; sheets stack and the router writes `?sheet=`, so back/forward and nested sheets (sheet over sheet) were not exercised; the Combobox fix was not tried inside every sheet that has one (multi-select in sheets: customer create, payment link, webhooks events, coupons).
- Other places that still **navigate** where a sheet is expected were found only by grep for two patterns (invoice and payment link); an audit of every `Link`/`navigate` that goes from a sheet context to another page was not done.
- Icons: only Usage changed; no audit of other duplicates in the sidebar and cards.

## Research first
1. Reproduce each of the five complaints in the **real stack** (not the mock): fresh sign-up, onboarding every screen, close tab mid-way, sign in on a second browser; long pages (invoices, customers) with a sheet; the multi-select in each sheet.
2. Audit: every `Link`/`router.navigate` reachable from inside a sheet or from a row action → list with "should open over (sheet) / should go to a page" decision.
3. List the sidebar and overview icons and find duplicates or confusing pairs.

## Decisions needed
Which destinations are allowed to leave the current page at all (a full customer or invoice view?). Proposal: nothing leaves the page unless the person clicks a clearly page-like link.

## Work
1. Reproduce and record the findings from the research step in this file.
2. Onboarding: every screen has a visible skip; no screen blocks the console; a person who skipped can resume from the checklist; verify with the real stack.
3. Tour: write the "seen" logic as a small state table (started, finished, skipped, save failed, second device) with a unit test per row.
4. Sheets: a single scroll-lock/restore mechanism owned by the Sheet component (not per page); test nested sheets and back/forward; Combobox inside every sheet.
5. Navigation audit fixes, one PR per feature area.
6. Icon audit fixes.

## Done means
Each complaint has a reproduction (before) and a passing real-stack check (after), plus an e2e where the mock can express it. Screenshots in the PR.

## Not verifiable here
Feel (scroll smoothness, focus) on real touch devices.

## Risks
Conflict with the other session working on the same files (#243 area): coordinate through branches, never edit the shared checkout.

## Findings (2026-10-29)

**Navigation audit** (read-only, every `Link`/`navigate` reachable from a sheet or row action):
- The one working model is the invoice: `peek.ts` (`peek:<kind>:<tenant>:<id>` in the sheet stack), `PeekHost`, `InvoiceRef`/`PeekInvoice`.
- Sheet-to-page jumps for records: **customer** (`CustomerRef`, in the invoice, subscription, payment and payment-link sheets; `CreditNotesCard`) — fixed in the customer-peek PR; **subscription** rows of the "related" lists — fixed too (`SubscriptionSheetById`, with what the list's sheet offers); **payment** rows (`RelatedRow` falls back to a link for kinds that are not peekable) — open, the payment detail is inline in two page variants and needs extracting first; the customer sheet's "Manage credits" button (`CustomersPage.toCredits`) goes to the credits page without the customer — open.
- Fine as they are: creating an app leaves for its dashboard, "Upgrade" links to the plan page, the App field of create sheets (keeps `?sheet=`), external URLs open a new tab.

**Icon audit:** a dozen clashes or wrong meanings fixed in the icon PR (sidebar pairs, dashboard, customer and key menus); `Send` for "Send test" and `ArrowUpRight` for "View" left as they are.

**Tour:** the "seen" states are in `docs/13-admin-ui.md` as a table, each row with its unit test.

**Still open in this plan:** ~~a peekable payment sheet~~ (done, see the status below); reproduction of all five complaints against the real stack (done by `e2e/complaints-real.spec.ts`, which passed in CI's end-to-end job on its first run, 2026-11-02, see the last status; a person's eyes and a real phone are still not); scroll-lock owned by the Sheet component and nested-sheet/back-forward tests; Combobox inside every sheet that has one; the three onboarding screens against "nothing is forced".

**Onboarding, read against "nothing is forced" (2026-10-29, by reading the code, not the real stack):** the wizard has three screens (welcome, app, done). "Skip for now" (`onb-skip`) is shown on every screen except the last, which ends with "Finish"; the only required fields (organisation and app name) block "Create", not skipping; a person who skipped resumes from the checklist on the home. Covered by `e2e-mock/onboarding.spec.ts`.

**Scroll (complaint 9):** `e2e-mock/sheet-scroll.spec.ts` opens a sheet on a scrolled customer list and checks the page behind does not move on open or on close. It passes, so the jump is not reproducible on this page in the mock; other lists, nested peeks and a phone are not covered.

## Review addendum (2026-11-01)
- **Status:** the "Still open" items of this addendum (payment peek, `toCredits`, the scroll lock owned by the stack, nested and back/forward e2e) are done; see the status section at the end of this file.
- The bullet that calls items 5, 6 and 13 unverified contradicts the Findings section; the Findings win.
- **Still open:** payment is not peekable (`related/PaymentRows.vue` falls back to a link); `CustomersPage.vue` `toCredits` does not pass the customer; the scroll lock is per page (`shared/lib/sheets.ts`), not owned by `Sheet`; nested and back/forward behaviour needs mock e2e.

## Status (2026-11-02, branch `feat/console-lists-peek`)

Done, with tests (`docs/13-admin-ui.md`, "Peeks and the page's scroll"):
- **Payment is peekable.** `payment` is in `PEEK_KINDS`; `PaymentSheetById` (found among the newest payments the console loads: there is no single-payment call) and `PaymentDetail` (shared with the payments page); `PeekHost` mounts it; `RelatedRow` of kind `payment` opens it instead of linking. `e2e-mock/peek-payment.spec.ts`: an invoice's payment opens over the invoice, the customer it names opens over the payment (three sheets, strip, back), a reload brings them back, an older payment says so.
- **`CustomersPage.toCredits` passes the customer** (`?customer=<id>`, environment kept), tested in the same spec.
- **The scroll lock is owned by the stack of sheets**, not the page: `syncScrollLock` in `shared/lib/sheets.ts` (called by `enter` and `leave`): `overflow: hidden` on the root while any sheet is open, the page put back at the position the first sheet found it when the last closes. Unit tests with a fake page (`sheets.test.ts`) and `e2e-mock/sheet-lock.spec.ts`: a wheel on the overlay (up and down) moves nothing, a nested peek and back/forward keep the lock, something that scrolls the page meanwhile is undone on close (within the header's own change of height, 12 px), leaving the page with a sheet open does not leave the next one locked. The test fails without the lock (checked once by disabling it).

Default (not agreed): the lock is `overflow: hidden` on `<html>` with the scrollbar's width kept as padding, not `position: fixed` on the body. iOS Safari ignores some `overflow: hidden` on the root in older versions: **not verified on a phone**.

**Still open:** ~~reproduction of the five complaints against the real stack~~ (the real-stack spec exists and passed in CI, see the last status; a person's eyes are still needed); Combobox inside every sheet that has one; the three onboarding screens against "nothing is forced" on the real stack; the browser's own back and forward (the address of the sheets is `replace`d, so the browser's history has none of them: "back" and "forward" are the strip's).

## Status (2026-11-02, branch `feat/console-bulk-more`)

Done in `web/admin` (no Go code). The five complaints of the request are each a mock e2e now (`e2e-mock/complaints.spec.ts`, with the older specs named in its header); what the mock cannot say (a fresh sign-up, the server's own record, a second browser, a real phone) is in `e2e/complaints-real.spec.ts`, which **only CI's end-to-end job runs** (no stack where it was written); its three tests passed on their first CI run (2026-11-02).

| Complaint | Covered by | Found |
|---|---|---|
| (5) onboarding forces things | `complaints.spec.ts`: every screen but the last has Skip, a half-typed form and a refused form do not block it, skipping makes nothing (no app, no rename) and the console opens at once, the last screen has Finish and no Skip; `onboarding.spec.ts` (resume, checklist, viewer, support switch, second browser, server down) | nothing wrong |
| (6) the tour keeps appearing | `complaints.spec.ts`: a tour that started by itself and was walked away from does not come back after another page, a reload, or a reload with the server down; `tours.spec.ts` (seen on the server, other browser, server down) | nothing wrong in the mock; the second-device case on a slow connection is still real-stack only |
| (7) Usage and Meters share an icon | `nav.test.ts` and `complaints.spec.ts`: no two items of one sidebar draw the same icon | **two clashes the earlier audit missed**, both from pages added since: *Connect your app* drew the rocket of *Go live*, and the *Audit log* the scroll of *Contracts*. Now a cable and a clipboard. The unit test fails without the change. |
| (9) a sheet scrolls the page to the top; the lists in a sheet | `complaints.spec.ts`: customers, subscriptions, invoices and the payment ledger at 1280 and 375 px: on open, on a wheel over the sheet and on close the page stays within the header's own change of height (12 px); `sheet-selects.spec.ts`: the searchable lists of the six sheets that have one (new customer, subscription, payment link, credit pack, expense, the person's new app) open, take the arrows and Enter, Escape closes the list and not the sheet, the page does not move, the scroll is locked once (the root, never the body as well) and given back on close; `sheet-lock.spec.ts`, `sheet-scroll.spec.ts` | nothing wrong. The sheets of a package, coupon, webhook endpoint and meter have native selects and suggestions only. Two things looked like bugs and were the test's own doing: a real click scrolls the page up to a header button first, and a wheel scrolls smoothly (a read in the middle of it is not "the page moved"). |
| (13) sheets that leave for another page | `complaints.spec.ts`: an invoice's, a customer's and a subscription's sheet hold no link to another page of the console except one that says it is a page (`...-open-page`); `peek-*.spec.ts` | nothing wrong |

**Still open:** ~~reproducing the five complaints against the real stack~~ (`e2e/complaints-real.spec.ts` has run: its three tests, a fresh sign-up through the real wizard, the server's own record of a tour and a second browser, passed on their first CI run on 2026-11-02, in the end-to-end job, which is also the only place they run), iOS Safari's handling of `overflow: hidden` on the root (not tried), the browser's own back and forward with sheets (the strip's are tested), and a screen reader's reading of the wizard and the tour. Sheets that hold a multi-value searchable list: none exists today (the multi-select Combobox is used in filters and the components playground, whose tests drive it).

## Status (2026-11-02, branch `fix/console-review-3`): the pages that had no tour

`docs/13-admin-ui.md` said every page of the sidebar has a tour; seven did not: the person's Help, Plan and billing, and the operator's Support inbox, Organisations, a tenant's Apps, Business address and E-invoice (and the platform plans, which are the packages page). Each has a short tour now (the page's title, then the one control it is about; sixteen strings in six languages), `app/tours.test.ts` builds every sidebar (a person's, the platform's, a tenant's) and fails for a page with none (the two doors, the operator console's and "My apps", excepted), and `tours.spec.ts` walks the new ones. **Not looked at on a phone or in Arabic.**

## Status (2026-11-02), branch `feat/leftovers-4-dx-console`: focus, the browser's history, the next record

The cases the earlier statuses listed as open or never wrote down, each in `e2e-mock/sheet-focus-history.spec.ts` (plus unit tests of the opener in `sheets.test.ts`); the behaviour is in `docs/13-admin-ui.md` ("Focus, the browser's history and the next record").

| Case | Found | Held by |
|---|---|---|
| Focus when a sheet opens from a row's "more actions" menu, and when it closes | **A bug.** The menu gave the focus back to its button as it closed, pulling it out of the sheet its "View" had just opened; the sheet then closed to the page (`<main>`), not to the row. Fixed: `RowActions` keeps the menu from taking the focus back when its action opened a sheet and offers its button to the stack (`rememberOpener`); the last sheet to close gives the focus to it (`restoreOpener`). A sheet opened by clicking a row behaves as before (the focus goes into the sheet; closing returns it to the page). | the spec's first test fails without the change |
| The browser's Back and Forward with a sheet open | nothing wrong, and written down: the sheet belongs to the history entry of the page it was opened on (`replace`), Back leaves that page and does not leave the page before it locked, Forward comes back to the entry and its sheet. The strip's back and forward are separate and were already tested. | the spec's second test (it passes before and after: it records behaviour) |
| The next record of a sheet (the arrows) | **A bug.** The sheet's body kept the scroll position of the record before: going down a list landed in the middle of the next customer. Fixed in `DetailSheet` (the body scrolls to its top when the index changes). | the third test fails without the change |

Defaults (not agreed): the browser's Back does **not** close a sheet (it would need a history entry per sheet, which the address design avoids on purpose: opening and closing sheets does not fill the history). If the owner wants Back to close the sheet on a phone, that is a decision with a cost (every open adds an entry, a reload with a sheet in the address has a Back that leaves the sheet's page).

**Still open:** the iOS Safari handling of `overflow: hidden` on the root, a screen reader's reading of the wizard, the tour and the sheet's focus (the focus moves were asserted in the DOM, not heard), Combobox inside a sheet on a real phone keyboard. Not verified: the new focus hand-over in Safari and Firefox (Chrome only); `focus()` on a button after a close animation is the browser's to honour.
