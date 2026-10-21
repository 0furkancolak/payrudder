# UI test report: a real-browser pass over the product

Date: 2026-10-21. Browser: Playwright Chromium (headless), against the local stack from `make dev` (gateway :42800, console :42301 running `main`; the
fixed frontend was served by Vite on :42311 against the same gateway). Nothing was mocked. Screenshots (desktop 1280, tablet 768, phone 375, light and dark)
were looked at, not only asserted.

The durable result is three real-stack specs in `web/admin/e2e/`:

| Spec | What it does |
|---|---|
| `product-real.spec.ts` | Signs a fresh person up through the API, creates an app, seeds the **sandbox** with real money (plan with a TRY and a EUR fee, four customers incl. a declined card and a EUR payer, `close-period`, the saga and the mock provider play the payments, a 30.00 refund to the card, expenses in TRY and EUR incl. a recurring and a voided one, a webhook endpoint, a credit pack, an ad hoc payment link, a portal link). Then checks in the browser: dashboard numbers per currency in both environments, live key create/see-once/revoke (and that the revoked key is 401), six languages (Arabic is `dir=rtl`), dark mode, phone/tablet/desktop with no sideways scroll and no console error or failed request, the hosted `/i/{token}` page paid with "Pay (test)", the portal `/c/{token}` selling the credit pack (balance 100 afterwards), sign out/in, wrong password, five wrong passwords then the right one (locked, same message), another person cannot see the app or the tenant, a person is not an operator, a revoked session goes back to sign-in. Takes about 3 minutes (the sandbox billing run starts ~30 s after the cutoff). |
| `operator-real.spec.ts` | Operator console: opens the console as the operator user (a real session of `user1@example.com`, listed in `QUOTIFY_OPERATOR_EMAILS`) and opens 16 tabs of a tenant that has data (overview, apps, customers, subscriptions, packages, pricing, payments, reports, credits, payment links, accounting, providers, e-invoice, mail, address, webhooks) at 1280 and 375: no console error/warning, no failed request, no sideways scroll. |
| `product.spec.ts` (mock) | One assertion updated (see "refund sign"). |

`make web-e2e` runs them with the others (`ADMIN_URL` selects the console, `SHOTS=<dir>` saves screenshots).

## Bugs found

| # | Severity | Where | What | State |
|---|---|---|---|---|
| 1 | medium | Credit packs page, payment links page (person and operator) | The currency field defaulted to **USD** whatever the tenant's currency, so a TRY shop that typed "50" for a pack or a link silently priced it in dollars. | Fixed: `useTenantCurrency` (apps of the session, or the tenant list for the operator) seeds the field once. |
| 2 | low | Dashboard, "Open webhooks" health pill | "1 endpoints, all fine" (no plural). | Fixed: plural forms (`one \| other`) in the six locales. |
| 3 | low | Dashboard KPI cards (Customers, Payments) | The sub-line "€25.00 per month · TRY 297.00 per month" was cut with an ellipsis, so the second currency was invisible on narrow cards. | Fixed: the line wraps. |
| 4 | low | Arabic (right to left) | Amounts rendered with the sign and the currency on the wrong side ("TRY 30.00-", "€ 25.00" before the digits). | Fixed: `formatMinor` isolates amounts in right-to-left locales (LRI/PDI); the refund row's minus is part of the formatted number (it was a separate "−" glyph). Unit test added. |
| 5 | low | API (`GET /v1/accounting/summary`) | The 400 message says "from_date and to_date are required" while the parameters are `from` and `to`. | Fixed in `billing-svc` (message only; the key and its translations are unchanged). |

No console error, console warning, page error or failed request was seen on any product page (desktop, tablet, phone, six languages, dark) or on any of
the 16 operator tabs, and no page scrolled sideways at 375 px. Cross-account checks (user B on user A's app, overview, keys, tenant via
`X-Quotify-Tenant`; a session on `/admin/v1/*`; a garbage token) all answer 404 / 401 as designed, in the API and in the browser.

## Numbers checked against what was seeded

Sandbox: two paid TRY invoices (198.00), one paid EUR invoice (25.00), one declined TRY invoice (99.00 outstanding), subscriptions TRY 297.00 + EUR 25.00
per month, one refund of TRY 30.00. The dashboard shows revenue TRY 198.00 and EUR 25.00 separately, "Billed TRY 297.00 / Collected TRY 198.00 / Outstanding
TRY 99.00", status counts that add up, the refund row and a live environment that is empty (it is another tenant). The operator accounting tab: EUR
income 25.00, expenses 10.00 (the monthly expense counted once), profit 15.00; TRY gross 198.00, refunds 30.00, net 168.00, expenses 207.77,
profit -39.77. A voided expense is out of every number. All correct.

## Observations that are by design or open

- **Revenue is gross of refunds** on the dashboard ("Revenue, last 30 days" 198.00 while 30.00 was refunded); the accounting tab shows the net. Payments
  "count" includes the refund row and every declined attempt (a declined invoice that dunning retries shows several failed rows). Not changed; worth a product decision.
- Credits page: the placeholder "Valid for (days, optional)" is cut in its input at 1280 px.
- Accounting chart legend overlaps the axis dates at 375 px (operator console).
- Hosted payment page right after "Continue": "Your payment is being processed" without any button until the invoice's saga reaches `AWAITING_PAYMENT`
  (a few seconds); the page refreshes by itself, so it works, but a first-time payer sees no hint.
- The customer portal's "View" / "Pay this invoice" links use the browser's default link colour (server-rendered page, not the console's look).
- The free tier's write limit (10 writes in a burst, then 429) is hit by any seed script that is not paced; the specs retry.
- Lockout: after five wrong passwords even the right password gets the same "wrong e-mail or password" text (by design, no oracle). The user is not told the account is locked or for how long.

## Not verified

- **The stack went away during the run** (the Go services started by `make dev` stopped, infrastructure containers stayed up; not stopped by this work). The last full green run of `product-real.spec.ts` covered
  tests 1-5; test 6 (cross-person, operator redirect, revoked session) failed once on a wrong URL in the test itself, was fixed (`/tenants` redirects to `/operator`) and the
  API half of it passed in a manual run, but **the corrected test 6 was not re-run end to end**. `operator-real.spec.ts` passed.
- Not exercised in the browser: creating/editing/deleting through every operator form (providers, e-invoice, mail, seller address, webhook failures and dead
  letters), the operator invoice/credit-note pages, translated validation errors of each form, expense create/edit/void through the accounting form (seeded through the API; the numbers were read in the UI),
  a recurring-expense edit, webhook deliveries that fail and are redelivered, a payment link in the live environment.
- Backend fixes were not run live (the stack runs `main`); the message change is covered by `go test ./services/billing-svc/internal/accounting/...`.
- Safari and Firefox, screen readers; contrast was judged by eye plus the documented 4.5:1 pill rule, not measured.
