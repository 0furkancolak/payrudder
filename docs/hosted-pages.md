# Customer-facing pages: states, data and what is still missing

The pages a customer sees without signing in: the portal (`/c/<token>`), an invoice (`/i/<token>`, `/c/<token>/invoices/<id>`), an ad-hoc payment link, a credit-pack purchase, a receipt, the bank-transfer instructions, and the messages (gone, cannot be shown). They are server-rendered Go templates in `services/gateway/internal/httpapi` (`hosted_page.go`, `portal.go`, `hosted_templates.go`, `hosted_csp.go`, the text in `hosted*_i18n.go`), with inline CSS, no script and no external resource. Plan: `docs/plans/04-hosted-pages-and-portal.md`.

## Where a state comes from

billing-svc decides the invoice state (`query.State`: paid, void, failed, open, processing from the invoice status and the saga); the gateway refines it with the payments-worker ledger (`refineState`: a pending charge means processing, for 30 minutes at most; a declined latest charge means failed). The page never computes a state on its own.

## Pages and states

| Page | State | What the customer sees |
|---|---|---|
| Invoice | open, payable | number, seller, lines, total, one pay button per method (card, bank transfer, test) |
| | open, no method | "No payment method is available right now. Please contact the sender." |
| | paid | "Paid", link to the receipt, PDF |
| | void | "Void" and, since the states PR, "This invoice was cancelled. Nothing is due." |
| | uncollectible | "Payment failed" and, since the states PR, "could not be collected … contact the sender" (it used to say "you can try again" with no button) |
| | failed / declined | "Payment failed … You can try again", pay buttons |
| | processing | "Payment processing" and a "Check again" link, no buttons. It used to reload itself every 5 s (`<meta http-equiv=refresh>`); axe fails that as WCAG 2.2.1 (F41), so the customer is asked to check again instead (default, not agreed: a script-free page cannot both refresh and pass). Continue on an ad-hoc link waits on the server (`startWait` 20 s, polled every 0.5 s) until the new invoice is out of processing before it redirects, so the usual payer lands on the pay buttons directly |
| | bank transfer | the bank block under the "open" badge (not a state of its own) |
| | requires action (3-D Secure) | **no state**: it happens inside the provider's page after the redirect |
| Invoice with the seller's agreements | open, a seller that requires contracts (`PUT /v1/contracts/settings`) | each text not accepted yet with an unticked box and its words, the two consents of a consumer (request to start at once, waiver), the payment method, and a button that says it is an order with an obligation to pay; the server checks the boxes (422 with the page again when one is missing or a text was replaced); fails closed when the agreements cannot be read ([19-contracts.md](19-contracts.md)) |
| Ad-hoc link | open, with promotion, refused code, single-use used | amount, code field, "Continue"; the discount; the reason; "already used" |
| Credit pack | open | what is bought, quantity, price, promotion |
| Portal | normal, empty | the subscription summary when there is one (plan, state, price, dates; read-only), invoices, payments, credits (balance, lots, packs to buy), test-mode banner |
| Receipt | paid | "Receipt", paid date and method, lines, print stylesheet |
| Bank-transfer instructions | after choosing it | reference and bank details |
| Gone | expired, revoked, not found, wrong scope | one message (the same page for all, deliberately: a link must not say which) |
| Cannot be shown | billing or ledger down | since the states PR, a page in the shared layout and the visitor's language, 503 with `Retry-After` (it was plain English text) |

Language: `?lang=`, else the customer's stored language, else `Accept-Language`, else English; six languages, Arabic right-to-left (`dir="rtl"`, logical CSS properties only, no letter-spacing on Arabic).

Numbers, money and dates follow the language (`pkg/localefmt`, the same table the invoice PDF uses): `1,234.56` and `2 November 2026` in English, `1.234,56` and `2. November 2026` in German, `1 234,56` (narrow no-break space) and `2 novembre 2026` in French, Spanish groups from five digits (`1234,56`, `12.345,67`), Turkish `1.234,56` and `2 Kasım 2026`. Arabic uses Latin digits, `.` and `,`, Arabic month names and a left-to-right mark before the minus (so `-5.00` keeps its sign on the left in right-to-left text). The digits are Latin because invoice numbers, IBANs and references are Latin on the same page; CLDR's own default for plain `ar` is Arabic-Indic digits, and switching is a table change in `pkg/localefmt` (UNVERIFIED with a reader of Arabic). Dates are the UTC day. The agreement text a customer accepts (`hosted_contracts.go`) still gets the order total as `1,234.56`: it is stored evidence rendered by plans-svc, not page text.

## Colour, dark mode, direction, accessibility

- Colours are custom properties (`lightColors` and the dark overrides under `prefers-color-scheme: dark` in `hosted.go`); a printed page puts the light tokens back, so a dark-mode reader's receipt is black on white. `TestHostedTextContrastMeetsAA` computes WCAG contrast for every text pair of both themes from the CSS the pages ship (oklch to sRGB), and the coloured side's white text at its three opacities; the focus ring is checked at 3:1.
- `TestHostedCSSHasNoPhysicalDirections` fails on `left`/`right` properties and on four-value shorthands with different left and right values.
- `TestEveryPageRendersInEveryLanguage` renders every template in the six languages with every optional part on, with `missingkey=error` (a text key a template names and the maps lack is an error), and checks `lang` and `dir`, a title, one `h1`, no `<no value>`, no `%!`, no unfilled placeholder, and that the page uses nothing its policy forbids (below). `TestHostedTextsAreCompleteInEveryLanguage` checks every text map has English's keys in every language and no forgotten English sentence.
- axe-core and screenshots: `web/admin/scripts/hosted-pages.mjs` (header of the file has the three commands). It opens every page the test wrote in Chrome, light and dark, at 1440, 768 and 360 px, runs axe (wcag2a, wcag2aa, wcag21a/aa, wcag22aa, best-practice) and fails on a violation or a page that scrolls sideways. It is **not** in CI: axe-core is not a dependency of the console (`npm i --prefix /tmp/axe axe-core` and point `AXE_CORE` at it).

## Portal data

The portal shows a **read-only subscription summary**, invoices, payments and credits. The summary comes from billing-svc's `GetPortalSubscriptions` (the portal token resolves to the tenant and the customer; billing reads plans-svc: `ListSubscriptions` for the customer's external id, `GetPlanVersion` for the price, `ListPlans` for the display name). The gateway asks for it beside `GetPortal` with a 2 s limit and shows the page without the section when it fails: plans-svc being down does not take the portal down. A portal link made before the tenant knew the customer by an external id has no section.

| Datum | Today |
|---|---|
| Subscription (plan name, state, interval, quantity, cancel at period end) | shown (trialing, active, past due, paused; a cancelled one is not listed) |
| Price | the plan version's fixed fee for the interval and currency times the quantity; no usage, coupon or tax; hidden when plans-svc has no fee for that interval and currency |
| Next date | only what plans-svc says exactly: the end of a trial, the day a waiting plan change takes effect (with the new plan's name), the end of a subscription that cancels with its period. **A plain active subscription has no "next payment" date**: its next fee date (`fee_due_at`) and billing anchor are not in the plans-svc `Subscription` message, and computing it in billing would repeat plans-svc's period arithmetic. `next_renewal_time` in the RPC is where it goes once plans-svc exposes it (an additive field on a public message: the owner's call) |
| Provider-owned subscription | shown with "Billed and renewed by the payment provider"; its dates are the provider's (`ProviderSubscription.NextBilledAt` in payments-worker is not read) |
| Card on file (brand, last4, expiry) | missing end to end; only an "update payment method" URL exists for provider-owned subscriptions (`PaymentMethodLink`, operator route) |
| Upcoming invoice (amount with usage and tax) | missing |
| Invoice and payment history, credit balance | present |

**No action is offered**: no cancel, resume, change plan or change card. Those are the owner's decision (product and legal), and the summary says "To change your plan or cancel, contact the seller." (default, not agreed). A test fails if the section contains a form or a button.

## Security headers

Every rendered page sends `Cache-Control: no-store`, `X-Robots-Tag: noindex, nofollow`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff` and a `Content-Security-Policy` built per page (`pageCSP` in `hosted_csp.go`): `default-src 'none'; style-src 'unsafe-inline'; [img-src 'self';] base-uri 'none'; form-action <targets>; frame-ancestors 'none'`. The view says what its page needs: `img-src 'self'` only when the page shows the seller's logo (served by the gateway itself); `form-action` is `'none'` for a page without a form, `'self'` for the ad-hoc page (promo, Continue) and the portal's pack form (Buy), and `'self' https:` for a payable invoice (its pay POST may end in the provider's https checkout, and a browser checks the redirect against `form-action` too). A response that is not a page (a redirect, a plain error) gets the closed policy. `style-src 'unsafe-inline'` is for the one `<style>` block; a hash of it would be stricter, but a proxy that rewrites inline CSS would then unstyle every page, so it is not done (default, not agreed). `TestEveryPageRendersInEveryLanguage` fails when a template uses a `<script>`, frame, object, `<link>`, inline `style=`/`on…=` attribute, `url()` or `@import`, an image the policy does not list, or a form the policy does not allow; `TestPagesSendThePolicyTheyNeed` checks the header the handlers send.

A provider script (Paddle.js, Stripe.js) would add `script-src`, `frame-src` and `connect-src` for the provider's hosts on the one page that embeds it (to be checked against the vendors' current documents when that is built), a per-request nonce or a same-origin script file, and probably a different `Referrer-Policy` on that page; the builder is where that goes.

## What is open (plan 04)

1. A next payment date for a plain active subscription (above), and the upcoming invoice with usage and tax. Customer actions (cancel at period end, resume, change plan, update the card through the provider's own page) are the owner's decision.
2. Card on file: needs the providers to tell us (brand, last4, expiry); nothing stores it today.
3. A requires-action state when an inline checkout is chosen (plans 06 to 08); the CSP builder is ready for it.
4. A language switcher (`?lang=` works; nothing on the page offers it).
5. The owner's eyes: the comparison with the Paddle/Stripe reference, the Arabic wording and layout (nobody who reads Arabic has read it), the German, French, Spanish and Turkish strings of the subscription summary and the "Check again" text (written by the model).
6. Real-browser matrix beyond Chrome, and a performance budget (the pages are one inline stylesheet and no script; not measured).
