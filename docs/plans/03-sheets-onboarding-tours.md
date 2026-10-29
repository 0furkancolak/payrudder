# 03 — Sheets, onboarding, tours, icons

## The request
List 1: (5) onboarding has needless forcing: steps must be skippable ("atla"); (6) the Payrudder welcome tour keeps appearing; (7) Usage and Meters need different icons; (9) on any scrolled page, opening a sheet scrolls the page behind it to the top (and back on close), and the multi-select inside a sheet does not scroll correctly; (13) some sheets navigate to another page instead of opening on top.

## Where we are
- #238: tour marked seen at start (`startTourOnce`), "Skip this step" in the old five-step wizard, Usage → `Activity` icon, sheet URL change uses `resetScroll: false`, Combobox scrolls only its list, invoice mentions and payment links open as sheets over the page (`PeekInvoice`).
- **#243 (another session) then rewrote onboarding into three screens and changed empty states / page headers.** The skip button and its test were part of the older wizard; the current state of 5, 6 and 13 after #243 has not been re-verified. The mock e2e for onboarding fails on main.

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

**Still open in this plan:** a peekable payment sheet; reproduction of all five complaints against the real stack (needs the compose stack and a person's eyes: not done here); scroll-lock owned by the Sheet component and nested-sheet/back-forward tests; Combobox inside every sheet that has one; the three onboarding screens against "nothing is forced".

**Onboarding, read against "nothing is forced" (2026-10-29, by reading the code, not the real stack):** the wizard has three screens (welcome, app, done). "Skip for now" (`onb-skip`) is shown on every screen except the last, which ends with "Finish"; the only required fields (organisation and app name) block "Create", not skipping; a person who skipped resumes from the checklist on the home. Covered by `e2e-mock/onboarding.spec.ts`.

**Scroll (complaint 9):** `e2e-mock/sheet-scroll.spec.ts` opens a sheet on a scrolled customer list and checks the page behind does not move on open or on close. It passes, so the jump is not reproducible on this page in the mock; other lists, nested peeks and a phone are not covered.
