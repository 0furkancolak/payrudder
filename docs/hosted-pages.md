# Customer-facing pages: states, data and what is still missing

The pages a customer sees without signing in: the portal (`/c/<token>`), an invoice (`/i/<token>`, `/c/<token>/invoices/<id>`), an ad-hoc payment link, a credit-pack purchase, a receipt, the bank-transfer instructions, and the messages (gone, cannot be shown). They are server-rendered Go templates in `services/gateway/internal/httpapi` (`hosted_page.go`, `portal.go`, `hosted_templates.go`, the text in `hosted*_i18n.go`), with inline CSS, no script and no external resource. Plan: `docs/plans/04-hosted-pages-and-portal.md`.

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
| | processing | "Payment processing", refreshes by itself every 5 s, no buttons |
| | bank transfer | the bank block under the "open" badge (not a state of its own) |
| | requires action (3-D Secure) | **no state**: it happens inside the provider's page after the redirect |
| Ad-hoc link | open, with promotion, refused code, single-use used | amount, code field, "Continue"; the discount; the reason; "already used" |
| Credit pack | open | what is bought, quantity, price, promotion |
| Portal | normal, empty | invoices, payments, credits (balance, lots, packs to buy), test-mode banner |
| Receipt | paid | "Receipt", paid date and method, lines, print stylesheet |
| Bank-transfer instructions | after choosing it | reference and bank details |
| Gone | expired, revoked, not found, wrong scope | one message (the same page for all, deliberately: a link must not say which) |
| Cannot be shown | billing or ledger down | since the states PR, a page in the shared layout and the visitor's language, 503 with `Retry-After` (it was plain English text) |

Language: `?lang=`, else the customer's stored language, else `Accept-Language`, else English; six languages, Arabic right-to-left. Numbers are always `1,234.56` and dates `YYYY-MM-DD` UTC (not localised yet).

## Portal data

The portal shows invoices, payments and credits. It has **no subscription section**: nothing returns the customer's subscription, next renewal, upcoming invoice or the card on file to a portal token.

| Datum | Today |
|---|---|
| Subscription (plan, state, interval, quantity, cancel at period end) | in plans-svc (`GetSubscription`/`ListSubscriptions`), **not reachable with a portal token** |
| Plan display name | missing (only the code) |
| Next renewal date | provider-owned: `ProviderSubscription.NextBilledAt` in payments-worker; Payrudder-owned: no field |
| Upcoming invoice (amount with usage and tax) | missing |
| Card on file (brand, last4, expiry) | missing end to end; only an "update payment method" URL exists for provider-owned subscriptions (`PaymentMethodLink`, operator route) |
| Invoice and payment history, credit balance | present |

## Security headers

Every rendered page sends `Cache-Control: no-store`, `X-Robots-Tag: noindex, nofollow`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff` and `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; base-uri 'none'; form-action 'self' https:; frame-ancestors 'none'`. A provider script (Paddle.js, Stripe.js) would need `script-src`, `frame-src` and `connect-src` for the provider's hosts (to be checked against the vendors' current documents when that is built), a per-request nonce or a same-origin script file, and probably a different `Referrer-Policy` on that one page; the CSP is a constant today, so it would become a per-page builder. Tests assert that no page carries a script.

## What is open (plan 04)

1. A portal-scoped read of the subscription, renewal date and upcoming invoice (new RPC in billing-svc/plans-svc; the portal token resolves to a customer id and the external id may be empty). Owner decision: view only, or cancel / resume / change card too.
2. Card on file: needs the providers to tell us (brand, last4, expiry); nothing stores it today.
3. A requires-action state and the CSP builder, when an inline checkout is chosen (plans 06 to 08).
4. Localised numbers and dates; a language switcher.
5. Visual review of every state at 360, 768 and 1440 px in light, dark and Arabic with axe, and a comparison with the owner's Paddle/Stripe reference: needs a person's eyes.
