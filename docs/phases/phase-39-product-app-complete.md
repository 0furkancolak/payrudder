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
