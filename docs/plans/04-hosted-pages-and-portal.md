# 04 — Customer-facing pages and portal

## The request
List 1 (10): the customer payment pages (e.g. `/c/MJ-KPFG…`) are still not modern; they must look and feel like **Paddle and Stripe** (the owner attached a Paddle portal screenshot: a coloured brand panel on the left; subscription, payment method and history on the right).

## Where we are
- #239: the server-rendered Go templates (`services/gateway/internal/httpapi/hosted_*.go`) got a split layout (seller and amount on a coloured side, the work on a calm pane), stacked full-width pay buttons, mobile and print rules. Checked with two screenshots (1440 and 390 px).
- The portal page (`/c/<token>`) shows invoices and payment methods. It does **not** show the subscription, the card on file, upcoming invoice, or let the customer update the card or cancel (billing-svc does not return them).

## Gaps
- The look was approximated from one screenshot; no comparison with Stripe Checkout / Customer Portal and Paddle's overlay and portal at the detail level (typography scale, spacing, states, error handling, 3-D Secure, wallet buttons, trust marks, success and failure pages, expired and paid states).
- Only the common template was restyled; each page state (invoice open, paid, void, expired link, payment failed, processing, bank transfer instructions, receipt, ad-hoc link, credit pack) was not reviewed one by one.
- No dark mode, no right-to-left check for Arabic, no accessibility pass (labels, focus, contrast, reduced motion), no real browser matrix, no performance budget (the pages are server-rendered with inline CSS: good, not measured).
- No custom brand colour/logo per seller beyond what existed.
- The portal lacks the Paddle-like content (subscription summary, next charge, payment method, history, cancel/resume).

## Research first
1. Walk Stripe's hosted invoice page, Checkout, Customer Portal and Paddle's checkout and portal with test accounts; write a table of the screens, the states, and the elements (and what each does) we would copy in structure, not in branding.
2. Read what the payment providers each need on our page (Paddle overlay/inline, iyzico checkout form, Stripe Checkout redirect, bank transfer) so the page's design does not fight them.
3. List the data the portal needs from billing-svc/plans-svc (current subscription, next invoice date and amount, payment method last4/brand, history) and which RPCs are missing.

## Decisions needed
- Design direction (the owner's screenshot is the reference: confirm which elements and what not to copy).
- Whether customers may **cancel/resume/change card** from the portal (product and legal decision) or only view.
- Seller branding limits (logo, one colour, fonts).

## Work
1. Write the state/screen inventory and the visual spec (tokens: colour, type, spacing, elevation, states) as a doc in `docs/`.
2. billing-svc/plans-svc: RPCs for the portal data; gateway view models.
3. Rebuild the templates state by state, each with a golden-file test of the rendered HTML structure and a screenshot at 360/768/1440 in light, dark, and Arabic.
4. Accessibility pass (axe against the rendered pages), print stylesheet for receipts.
5. Portal actions the owner approves (card update through the provider's own page, cancel at period end).

## Done means
Every state has a screenshot in three widths and two themes attached to the PR, passes axe, and the owner has compared it with the reference.

## Not verifiable here
Visual judgement ("modern") is the owner's; real card flows need provider accounts (06–08).

## Risks
Provider scripts (Paddle.js, Stripe.js) bring their own CSP needs; the page's Content-Security-Policy must be designed with them.

## Review addendum (2026-11-01)
- The portal shows invoices, payments and credits (`docs/hosted-pages.md` is the inventory), not payment methods. #268 added the state table, the 503 page, the void/uncollectible text and `hosted_states_test.go`.
- Confirmed open: no `prefers-color-scheme` in `hosted_templates.go`; the CSP is a constant; numbers and dates are not localised.
- **Split the portal item:** (a) a read-only subscription summary needs a billing-svc read RPC and no product decision; (b) customer actions (cancel, change plan, update payment method) are the decision.
- **Dependency:** plan 21 puts a contract checkbox on the same templates; land 21's server-side gate before restyling them.

## Status (2026-11-02): the checks, the CSP builder, localised numbers and the subscription summary are built (branch `feat/hosted-pages-portal`)

Built (details and the test names are in [hosted-pages.md](../hosted-pages.md)):

- **Dark mode and print.** The pages already had `prefers-color-scheme: dark` tokens (the review addendum's "no `prefers-color-scheme`" was wrong about `hosted.go`; it is not in `hosted_templates.go`). Added: the light tokens as one constant that a printed page puts back (a dark-mode reader's receipt was grey on white), `prefers-reduced-motion`, the focus ring darkened to 3:1 in the light theme (it was 2.7:1), a focus outline and tab stop on the scrolling table regions (axe: `scrollable-region-focusable`). `TestHostedTextContrastMeetsAA` computes WCAG AA for every text pair in both themes from the shipped CSS and for the side panel's white text at its three opacities (all passed except the ring).
- **Right to left.** Physical `margin`/`padding` on the pay form and the tables became logical; Arabic gets no letter-spacing (tracking breaks joined letters) and a larger size (the small-caps labels were 12.5 px). `TestHostedCSSHasNoPhysicalDirections` guards it.
- **Every page, every language.** `TestEveryPageRendersInEveryLanguage` (`lang`/`dir`, title, one `h1`, strict template keys, no `<no value>`/`%!`, nothing the policy forbids) and `TestHostedTextsAreCompleteInEveryLanguage` over all eight text maps.
- **axe and screenshots.** `web/admin/scripts/hosted-pages.mjs` on the console's Playwright with Chrome: 11 page variants x 6 languages x light/dark x 1440/768/360. Findings fixed: the processing page's `<meta http-equiv=refresh>` (critical, WCAG 2.2.1), an empty table header, scrolling regions without a tab stop, the focus ring, the portal scrolling sideways at 360 px (a one-column grid track as wide as its widest table: `minmax(0,1fr)`, and an absolutely positioned label escaping the scroll box), the credit lots table whose first column was right-aligned into the middle, the content pane left-aligned in a wide column (now centred).
- **Localised numbers and dates.** `pkg/localefmt` (en, tr, de, fr, es, ar) is the one table; the invoice-worker's layouts now embed it (its golden PDFs are unchanged) and the hosted pages use it for every amount and date. Golden tests for the six languages.
- **Per-page CSP.** `pageCSP` (`hosted_csp.go`); each view says what it needs; the adhoc page lost `https:` in `form-action` and the pages without a logo lost `img-src`.
- **Portal subscription summary (read-only).** billing-svc `GetPortalSubscriptions` (proto, `hosted/command/portalsubs.go`, handler), the gateway section in six languages; unit tests for the mapping, a billing integration test (`PortalTokensStayWithinTheirTenantAndCustomer`), gateway tests including "plans-svc down: the page still shows" and "the section has no form or button". No customer action was added.

Defaults taken (not agreed; the owner may overturn them):
1. **The processing page asks to check again instead of reloading itself.** A script-free page can only reload with `<meta http-equiv=refresh>`, which fails WCAG 2.2.1 (and the European Accessibility Act covers consumer e-commerce). Cost: a customer who just paid sees "processing" until they press the link, and the ad-hoc "Continue" lands on "processing" for the few seconds the invoice PDF takes (the reload used to hide that); if it bites, the `/start` handler can wait for the open state before it redirects (not built). The alternative is a same-origin script file with `script-src 'self'` (stop/start control), which the CSP builder now makes a one-page change.
2. **Arabic digits are Latin** (see hosted-pages.md); Arabic-Indic digits are a table change.
3. **No "next payment" date for a plain active subscription**: plans-svc does not expose `fee_due_at` or the billing anchor and billing would have to repeat its period arithmetic (anchor = the start for advance fees, the month start for arrears, kept through plan changes). Shown instead: trial end, a waiting plan change's day, the end of a subscription that cancels with its period. Exposing a next fee date is an additive field on plans-svc's public `Subscription` message: the owner's call.
4. **The summary says "To change your plan or cancel, contact the seller."** Cancel, resume, change plan and change card are the owner's decision and are not built.
5. `style-src 'unsafe-inline'` stays (a hash would unstyle the pages behind a proxy that rewrites inline CSS).
6. The pane is centred in its column and the agreement texts' order total keeps `1,234.56` (stored evidence).

Not verified: the comparison with the owner's Paddle/Stripe reference (a person's judgement); Arabic read by a person (layout mirrors, nothing overflows, wording unchecked); the German, French, Spanish, Turkish and Arabic strings of the summary and of "Check again" were written by the model; Safari and Firefox; a real phone; the provider flows (Paddle.js, iyzico form, 3-D Secure) which need accounts (plans 06 to 08); the portal summary against a real plans-svc (the unit and integration tests use fakes of it). axe runs in CI since 2026-11-02 (the `hosted-pages` job of `ci.yml`, see [hosted-pages.md](../hosted-pages.md)). Screenshots are not committed (the script makes them).

Still open from the list above: the Paddle-like portal's next charge for an active subscription, card on file and upcoming invoice (data missing end to end); customer actions; a language switcher; a performance budget; seller branding beyond the logo.

