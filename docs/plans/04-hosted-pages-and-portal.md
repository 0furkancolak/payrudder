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
