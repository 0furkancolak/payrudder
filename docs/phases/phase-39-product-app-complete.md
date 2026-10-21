# Phase 39 — The product app, completed: proven addresses, password reset, members and the billing pages

## Goal

[Phase 38](phase-38-product-app.md) let a person sign up, open an organisation, create apps and watch their payments, and listed what it left out. This phase builds those parts so the product app is complete for an end user:

1. **E-mail verification and password reset**, through a mail path Quotify itself owns for mail to *people* (not to a tenant's customers).
2. **Invitations and members**: invite by e-mail, accept, change a role, remove a member, never lose the last owner.
3. **The billing pages for people**: invoices (list, detail, PDF), the payments ledger, packages, reports, under `/app/t/{tenant}`, backed by routes a session may call; operator-only routes stay out of the app.

It shipped in three pull requests, in that order.

## 1. E-mail verification and password reset

### The mail path

Mail Quotify writes to a person (a verification link, a reset link, an invitation, a warning) is sent by **plans-svc** itself, through the same SMTP code the notifier uses (`pkg/mailx`, moved out of the notifier; the notifier keeps thin aliases). It is separate from the tenant mail switch on purpose: the switch (phase 26/33) decides whether a *tenant's* mail to *its customers* is delivered; mail to people is the platform talking to its own users and is always sent.

| Setting (YAML default, env overrides) | Default | Meaning |
|---|---|---|
| `SMTP_ADDR` | `localhost:42025` | SMTP relay without authentication (the dev Mailpit; UI at <http://localhost:42825>). Compose sets `mailpit:1025`. |
| `MAIL_FROM` | `Quotify <no-reply@quotify.local>` | The sender. |
| `QUOTIFY_APP_URL` | `http://localhost:42301` | The console's public address; the links in mails open `<url>/verify`, `/reset`, `/invite`. |

Sending is **in the background** (`identity/usermail`): the request never waits for the relay, at most eight mails are in flight and one more waits half a second before it is dropped and logged, and a failed send is logged without the address. A lost mail is something the person asks for again (resend, forgot password); there is no outbox because every mail has a "send again" path and a stored token that would otherwise be wasted. Texts exist in the six console languages (`en`, `tr`, `de`, `fr`, `es`, `ar`, the last right-to-left); the language is the `locale` the page sends, else `Accept-Language`, else English, and the user's choice at sign-up is stored for later mails. Names in a mail are escaped in the HTML, stripped of line breaks, and the subject carries no name from outside the invitation's account name.

### Tokens

`user_tokens` (migration `00019_email_tokens.sql`; also `users.email_verified_at`, `users.locale`, `users.taken_notice_at`). A token is `qv_` (verify) or `qr_` (reset) plus 256 random bits, URL-safe. **Only its SHA-256 is stored.** It is **single use**: spending it is one `UPDATE … WHERE used_at IS NULL AND expires_at > now() RETURNING`, so of two concurrent requests exactly one succeeds (tested with parallel calls). Verification links live **24 hours**, reset links **1 hour**. Asking for a new link of a kind invalidates the older ones. A used, expired, replaced, unknown or wrong-kind token is the same `400 identity.token.invalid`.

The mailed link carries the token in the **URL fragment** (`/reset#token=…`). A fragment is never sent to a server, so the token is in no nginx or gateway log and no `Referer`; the page reads it once and removes it from the address bar and the history entry. The token is never a query parameter.

### Flows

- **Sign-up** (`POST /auth/signup`) creates user, account and owner membership and mails a verification link. It opens **no session** and answers **`202` whether the address was free or taken**: for a taken address the owner is mailed "somebody tried to sign up with your address" instead (at most one an hour), and the password is hashed in both cases so the time does not tell either. This closes the leak phase 38 documented ("sign-up reveals a taken address"). The web page signs in with the password just typed: for a new address that works at once; for a taken one it fails and the page shows "check your inbox", the same text either way.
- **Unverified people may use the app.** They can do everything except **create a live-environment API key** (`403 identity.email.not_verified`; sandbox keys need nothing). Rationale: live keys move real money, so the address must be provably reachable; everything else is harmless to try. The console shows a banner with a resend button on every page and disables the live-key button with a reason. Inviting members also requires a verified address (see below), so an unproven account cannot be used to mail strangers.
- **Verify** (`POST /auth/verify {token}`): marks the address proven. Public (the token is the credential), 20 a minute per client address.
- **Resend** (`POST /auth/resend-verification`, session required): at most one a minute (`429 identity.verify.throttled`, kept in the database so it holds across replicas); a verified address gets no mail.
- **Forgot password** (`POST /auth/forgot {email}`): always `202` for a well-formed address; a link is mailed only when the address has an account and none went out in the last minute, so it cannot flood an inbox. 5 a minute per client address. The only refusal, a malformed address, depends on the input alone. Known limitation: the unknown-address path skips a database insert, so a very precise timing attack could tell the two apart; both do the same work up to the lookup and the mail is sent asynchronously, which keeps the difference at about one INSERT.
- **Reset** (`POST /auth/reset {token, password}`): single use; checks the token before hashing the new password (so a bad token does not cost a 19 MiB hash), then in one transaction spends the token, sets the password, clears the sign-in lock, marks the address verified (the person just proved the mailbox), **revokes every session of the user** and invalidates other reset links. It opens no session; the person signs in. A reset is also how someone who holds a mailbox dislodges a squatter who registered the address first.

### Threat notes

| Threat | Stance |
|---|---|
| Account enumeration through sign-up | Same `202` and same time; the owner is warned instead. A visitor who signs up *and then signs in* with a password of their choice learns whether the address was free (it succeeded) — but only by creating an account, which the per-address limits make expensive, and the real owner can take it back with a password reset. A fully closed variant (no session until the mail link is followed) was rejected because unverified people are meant to use the app. |
| Enumeration through forgot-password | Same `202`; throttled by address and by client address. |
| Leaked database | Tokens are hashes of 256 random bits; passwords are argon2id. |
| Token in logs/Referer | Fragment only. |
| Stolen session after a reset | All sessions are revoked by the reset. |
| Mail flooding through sign-up / forgot | One warning an hour, one reset mail a minute, per-address gateway limits, a bounded in-flight sender. |

### Verification

- `internal/itest/verify_integration_test.go` (real Postgres): hash at rest and lifetimes (24 h / 1 h), resend throttle and replaced links, garbage / wrong-kind / session-token / expired tokens, single use under parallel requests (verify and reset), unknown address vs known (no mail, same answer), reset revokes sessions and clears the lock, the old password dies, reset proves the address, language of the mail, the taken-address warning and its hourly limit (in `identity_integration_test.go`), live key refused until verified.
- `usermail` unit tests: six languages for every mail with equal placeholders, escaping and header safety, right-to-left Arabic.
- Gateway tests: 202 for any address, locale choice, session required for resend, throttling, rate limits, the body cannot name a user.
- Vitest (fragment token reader, error keys in six languages, locale parity) and mocked Playwright specs (taken-address notice, forgot → reset, used link, banner → resend → verify → live key enabled).
- **Not verified:** delivery through a real SMTP relay other than Mailpit; the mail clients' rendering (HTML is simple, inline-styled, with a text part); Kubernetes values only pass `SMTP_ADDR` (set `QUOTIFY_APP_URL` for the plans-svc deployment).

## 2. Members and invitations

An account is a team. The schema had `account_members` since phase 38 but only the creator was ever in it; now owners and administrators invite people, and members can change roles, leave or be removed.

### Who may do what

| | viewer | admin | owner |
|---|---|---|---|
| See the members | yes | yes | yes |
| See, send, resend and cancel invitations | no | yes (admin and viewer invitations) | yes (all) |
| Invite as owner / make or unmake an owner / remove an owner | no | no | yes |
| Change an admin's or viewer's role, remove an admin or viewer | no | yes | yes |
| Rename the account | no | no | yes |
| Leave | yes | yes | yes, unless the last owner |
| Create apps and API keys | no | yes | yes |

The checks live in plans-svc (`identity/command/members.go`, `mayHandle`) and in SQL through `account_members`; the gateway only passes the session's user and an account that is one of the session's. Another account's members, invitations and ids are "not found" (404), never "forbidden", so ids cannot be probed.

### Invitations

`account_invites` (migration `00020_invitations.sql`). An invitation is a `qi_` token (256 random bits) mailed to an address; **only its SHA-256 is stored**; it lives **7 days**; the link is `<app>/invite#token=…` (fragment, like the other mailed links). There is at most one open invitation per address and account (a partial unique index); inviting an address again, or pressing "send again", **replaces the link** (the old one stops working), at most once a minute (`identity.invite.throttled`). An invitation that ran out does not block a new one to the same address.

- **Inviting needs a verified address** (`identity.email.not_verified`): an unproven account cannot be used to mail strangers. An account may have 50 open invitations and make 30 a day (`identity.invite.quota`). An address that is a member already is `identity.invite.already_member`.
- **The accept page** (`/invite#token=…`) asks plans-svc what the token is for (`POST /auth/invitations/lookup`, the token in the body and never in a URL) and then asks for the right thing:
  - an address **without an account** chooses a password (`POST /auth/invitations/accept`): a user is created with the address already verified (the mail proved it), made a member with the invited role, and signed in. They get **no account of their own**, only the invited one;
  - an address **with an account** signs in, then joins (`POST /app/v1/invitations/accept`); the server requires the invitation to be for the **session user's own address** (`identity.invite.wrong_user`), so a token for another address cannot be used by a different account, and the public accept refuses such an address with `identity.invite.sign_in_required` instead of creating a second user;
  - somebody already signed in with the invited address joins with one click; with another address the page says so.
- Spending an invitation is one atomic UPDATE (`accepted_at`), so two requests with one token produce one member (tested in parallel).
- **People with several accounts** (an invitation adds one) choose the account they work in with a switcher; the choice is kept in this tab and sent as `?account_id=`, which the gateway checks against the session. A person who left or was removed from their last account sees an explanation instead of errors.

### The last owner

An account always keeps one owner. Every change that could remove one (demoting an owner, removing an owner, an owner leaving) takes `pg_advisory_xact_lock` on the account, **re-reads the caller's role under the lock**, counts owners and refuses with `identity.member.last_owner` (409) when none would remain. Without the lock two owners demoting each other at the same moment could both see "another owner remains". The tests run exactly that race.

### Other points

- Removing a member takes effect on the next request: access is a membership join in SQL on every call, there is no cached role. The removed person's sessions stay valid (they are theirs) but find nothing of the account.
- **Not built:** transferring ownership in one step (promote the new owner, then demote yourself), deleting an account, an audit log of member changes (the changes are logged with ids), seats or billing for members.

### Verification

- `internal/itest/members_integration_test.go` (real Postgres): who may invite and manage, refresh / throttle / expiry / revoke / resend of invitations, hash at rest and 7-day lifetime, accepting as a new user (parallel single use, language, session) and as an existing user (wrong user, sign-in required, proves the address), quotas, roles under every actor, leaving, the last owner including the demotion race, removal cutting off apps and tenants, and strangers (other accounts) getting "not found" from every route.
- Gateway tests: session required on every member route, the acting user and account come from the session and not from a body, another account is refused before plans-svc is asked, viewers read and leave only, the public lookup / accept routes and their per-address limit.
- Mocked Playwright: invite, resend, cancel, role change, remove with confirmation, the last owner, viewer / administrator views, rename, the four accept-page cases, the account switcher, no-account state.

## 3. The billing pages for people

Phase 38 gave people the customers, payment links, credits and webhooks pages (the console's own pages, mounted under `/app/t/{tenant}`) and left out the pages whose console versions call operator-only endpoints. Now a person has, under one tab bar per environment: **Customers, Invoices, Payments, Packages, Pricing, Reports, Payment links, Credits, Webhooks.**

### Which route serves which page

| Page | Calls | Notes |
|---|---|---|
| Invoices (list, status filter, load more) | `GET /v1/invoices` | Tenant route that existed already; a session reaches it by acting as its own tenant (`X-Quotify-Tenant`). |
| Invoice (lines, totals, payments recorded by hand, PDF) | `GET /v1/invoices/{id}`, `/pdf` | The PDF is a presigned link (five minutes) opened in a new tab. |
| Payments | **`GET /v1/payments`** (new) | The tenant's ledger as a person reads it: charges and refunds, each folded to its settled outcome (paid, processing, failed, refunded), newest first, minor-unit strings per currency, `truncated` when the newest 1000 ledger rows were all read, `503` when payments-worker does not answer. No provider references, no decline details, no payouts or disputes. |
| Packages | `GET /v1/plans`, `/v1/plans/{code}/versions/{version}` | Publishing is `POST` on the same routes (owner and administrator). |
| Pricing (coupons, retry schedule, tax rates) | `/v1/coupons`, `/v1/dunning-settings`, `/v1/tax-rates` | |
| Reports | `/v1/reports/revenue`, `/subscriptions`, `/dunning` | A row per currency, never one total. |

The new route is a tenant route, not an `/app/v1` one: it is useful to API-key holders too, it needs no second authorisation path, and "a session acting as its own tenant" is already tested for every `/v1` route. The tenant whose ledger is read is the **principal's** (from the API key, or from plans-svc's membership-checked answer for a session); nothing in the request can name another (tested with a foreign tenant id in the query).

### What stays operator-only, and is not in the app

The saga timeline, the transfer controls, recording a payment by hand, issuing credit notes, the raw ledger (`/admin/v1/tenants/{id}/ledger`) and the reconciliation, the operator's tenant lookup (`/admin/v1/tenants/{id}`), provider accounts and routing, dead letters, replays. The product pages for invoices and payments are new components (`features/product/InvoicesPage.vue`, `InvoiceDetailPage.vue`, `PaymentsPage.vue`, API in `billingApi.ts`) for exactly this reason: the console's versions call those routes. Two guards keep it that way:

- `features/product/appBoundary.test.ts` (Vitest) reads the sources of every page mounted under `/app/t` and fails when one mentions an `/admin/v1` route or the operator's `useTenant`. (It found one: the packages page asked for the tenant's currency through `/admin/v1/tenants/{id}`; it now uses `useTenantCurrency`, which reads a person's apps instead.)
- A mocked Playwright spec visits every tab and asserts that no `/admin/` call was made and the session survived (an operator route answers a session `401`, and a `401` signs the person out).
- Gateway tests assert that a session gets `401` on the operator copies (`/admin/v1/tenants/{id}/ledger`, `/invoices`, `/pdf`, `/saga`, `/reconciliation`, `/provider-accounts`, the tenant lookup, recording payments, credit notes), with and without naming a tenant.

### Viewers

A viewer's session may only `GET` (the gateway refuses any other method with `403 identity.account.forbidden` before the handler runs: tested for packages, versions, coupons, tax rates, dunning, customers and invoice links). The pages follow: the tenant layout works out the person's role in the account that owns the tenant and provides it (`shared/lib/access.ts`); the packages form is hidden for a viewer, the pricing page is wrapped in a disabled `<fieldset>` (its forms stay visible and readable, switched off), and a note says the role is read-only. The server remains the authority; the UI only avoids offering buttons that would be refused.

### Cross-account tests

`billing_pages_test.go` (gateway) asks every read these pages make, plus the pages mounted earlier, as Ada acting on Bob's live and sandbox tenants, as Bob acting on Ada's, with a session and no tenant, and with no, expired and unknown credentials: always 404 `identity.tenant.not_found` or 401, and no ledger is read for a refused request. The plans-svc side of the same promise (a membership join in SQL for `ResolveMyTenant`) is the phase 38 integration test.

### Not built

Credit notes and refunds on the invoice page (read-only credit notes for people would need the `/v1/credit-notes` reads wired into the page), the subscriptions page (its provider-run panel is operator-only), e-invoices, the seller address and the mail switch for people. Each is a candidate for a later phase; none is stubbed.

### Verification

- Gateway: `billing_pages_test.go` (above) and the `/v1/payments` fold, order, limits, truncation, `503` and tenant-from-principal tests; OpenAPI contract test.
- Vitest: the boundary test, locale parity for the new keys.
- Mocked Playwright (`e2e-mock/product.spec.ts`): invoices (filter, load more, open, PDF), payments, packages / pricing / reports for an owner, the same for a viewer, every tab without an operator call; the existing `billing.spec.ts` packages tests now provide the tenant list the page reads its currency from.
- **Not verified:** the pages against a real gateway and payments-worker end to end (only the mocked contract and the gateway tests with a fake payments-worker); Safari and Firefox; the PDF link in a real browser (the test checks the URL passed to `window.open`).
