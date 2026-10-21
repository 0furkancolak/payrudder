# Phase 38 — The product app: people, accounts, apps and what happens in them

## Goal

Until now the only console was the **operator's**: one shared token, every tenant. The people Quotify is for — a developer or a company that signs up, opens an account (an organisation), creates projects ("apps", each with a sandbox and a live environment) and wants to watch each app's payments and status — had no way in. This phase builds that product experience: **sign up, sign in, an organisation home with app cards, an app dashboard, API keys, account settings**. The operator console stays and takes the same look.

It shipped in two pull requests: identity and gateway first, the web app second.

## Scope

**In:**

1. **Identity** (plans-svc, module `internal/identity`, `IdentityService`): users with argon2id password hashes, `account_members` (owner, admin, viewer), opaque revocable sessions (`qs_…`, hash only stored, 14 days sliding), sign-in throttling and lockout.
2. **Gateway**: public `POST /auth/signup` and `/auth/signin` (rate limited per address), `POST /auth/signout`, `GET /auth/me`; session routes `/app/v1/account`, `/app/v1/apps` (list, create), `/app/v1/apps/{id}`, `/app/v1/apps/{id}/api-keys` (list, create-once, revoke), `/app/v1/apps/{id}/overview`.
3. **A session can act on a tenant** of `/v1/*` (`X-Quotify-Tenant`) only when the tenant belongs to an app of one of the person's accounts; a viewer may only read. Sessions are never operators.
4. **OpenAPI** for all of it; error keys (`identity.*`).
5. The web app (second PR).

**Out (follow-ups, not stubbed):**

- **E-mail verification and password reset** (built in [phase 39](phase-39-product-app-complete.md); this is what it said when it was left out). Both need a mail path for people (not for a tenant's customers): the notifier today sends a tenant's invoice mail through the tenant's mail switch (phase 26/33) and has no template or sender identity of its own for Quotify-to-person mail. Doing it properly means a sender configuration, templates in six languages and a token table; it is not cheap, so it is not here. Until then a forgotten password has no self-service path (an operator would have to replace `users.password_hash` by hand) and an address is not proven to belong to the person. Sign-up therefore tells a visitor that an address is taken (it cannot hide it without a mail to the owner) — the per-address limit and the lock are the answer to enumeration and guessing.
- Inviting members, changing a role, removing a member, transferring ownership (built in phase 39; ownership moves by promoting the new owner and demoting the old one). The schema and the checks (roles) are there; only the API and screens are missing.
- Deleting or renaming an app (no API for operators either).
- Multi-factor authentication. (Sign-in with Google and a one-time e-mailed code were built in [phase 41](phase-41-signin-methods.md).)
- Platform-wide dead letters in the app overview: the queues belong to the platform, not to one tenant, and showing them to a person would leak other tenants' health. They stay in the operator console.

## Design

### Model

```
users ──< account_members >── accounts ──< apps ──< tenants (sandbox, live) ──< api_keys
users ──< user_sessions
```

`accounts`, `apps`, `tenants` and `api_keys` already existed (phase 25); this phase adds `users`, `account_members` and `user_sessions` (migration `00018_identity.sql`). None of the new tables has a `tenant_id`, and `plans_app` has no grant on them: only the identity module, on the system pool (`plans_system`), reads or writes them. The tenant stays the unit of isolation (RLS); the membership is what lets a person reach a tenant.

### Session model

- `SignUp` and `SignIn` return a token once: `qs_` + 43 characters of base64url (256 random bits). The database stores `sha256(token)` only (`user_sessions.token_hash`, unique). A leaked database or backup cannot be replayed as sessions, and the token needs no salt or stretching because it is not a human secret.
- Every product request carries `Authorization: Bearer qs_…`. The gateway asks plans-svc `GetSession` on every request: one primary-key lookup of the hash, `revoked_at IS NULL AND expires_at > now()`. The gateway keeps no session cache, so **sign-out, revocation and expiry take effect on the next request**, on every replica.
- Expiry slides: 14 days from the last use, written at most every 5 minutes (the update is only done when `last_seen_at` is older), and never beyond 90 days from sign-in (`MaxLifetime`). Old and revoked sessions of a user are deleted when that user signs in again.
- `SignOut` revokes the token's row; signing out twice, or with an unknown token, is not an error.
- Bad, malformed, unknown, revoked and expired tokens are all the same `401 identity.session.invalid`. An unreachable plans-svc is `503`, never "signed out".

### Passwords

- argon2id, **m = 19 MiB, t = 2, p = 1** (the OWASP minimum for argon2id; salt 16 bytes, key 32 bytes), stored as a PHC string `$argon2id$v=19$m=19456,t=2,p=1$salt$hash`. The parameters travel with the hash, so they can be raised later and old hashes still verify (and `Verify` refuses hashes asking for more than 256 MiB, so a damaged row cannot make a request allocate gigabytes).
- **Why these numbers:** the platform's standing rule is low RAM. 19 MiB and two passes cost roughly 20–40 ms of one core and 19 MiB while the hash runs; a pod sized for a gateway-class service can afford a few at a time. A stronger setting (64 MiB, t=3) would put several hundred MiB behind a burst of sign-ins. Because the cost is per attempt, **concurrent hashes are limited to four per plans-svc replica** (a semaphore: the rest wait, honouring the request deadline), so a flood of sign-in attempts queues instead of exhausting memory. Together with the per-address and per-account limits below the attacker's budget is small.
- Passwords are 10 to 256 bytes (the cap keeps a request from making the hash read megabytes). No composition rules; length is what matters. The e-mail is trimmed and lower-cased; uniqueness is `lower(email)`.
- Passwords, tokens and key plaintexts are never logged; log lines carry ids only.

### Sign-in hardening

- **One answer for every failure.** Wrong password, unknown e-mail and locked account all return `401 identity.auth.invalid_credentials` with the same body, so the endpoint does not tell which addresses exist.
- **Timing-safe unknown-user path.** An unknown e-mail (and a locked account) runs a real argon2id verify against a dummy hash with the same parameters before answering, so the response time does not give the address away. Hash comparison is `subtle.ConstantTimeCompare`.
- **Lockout.** Five wrong passwords lock the user for 15 minutes (`failed_logins`, `locked_until`, kept in one atomic `UPDATE … RETURNING`). While locked, the password is not even checked and the attempt is not counted, so an attacker cannot extend a lock forever; when the lock runs out the count starts again at one. A successful sign-in resets it. The cost: an attacker who knows an address can keep that person locked out by trying every 15 minutes. That is the usual trade for a system with no second factor and no mail to unlock; the per-address limit makes it expensive, and the operator can clear `locked_until`.
- **Per-address limits at the gateway:** 10 sign-ins and 5 sign-ups a minute per client address. Behind the console's nginx the connection address is the proxy's, so the gateway honours `X-Forwarded-For` **only** when `QUOTIFY_TRUST_FORWARDED_FOR=true` **and** the connection itself comes from a private or loopback address; nginx overwrites the header with `$remote_addr` (it is the edge), so a visitor cannot choose their own bucket. Without the setting the header is ignored.

### Authorisation

The gateway never takes a user, account or tenant id from a request body: the user comes from the resolved session, the account from `?account_id=` (checked against the session's memberships) or the first membership, the tenant from plans-svc's answer. **plans-svc checks again, in SQL**: every account-scoped query joins `account_members` on the user id (`GetAppForUser`, `GetTenantForUser`, `GetMembership`), so a request for another account's app, tenant or key finds no row, whatever the gateway sent. Other accounts' objects are *not found* (404), not *forbidden*, so ids cannot be probed. Roles: **owner** and **admin** create apps and keys and revoke keys; **viewer** reads. A viewer acting on a tenant through `/v1/*` may only use GET.

A session reaches `/v1/*` only with `X-Quotify-Tenant`, and the tenant is resolved through the membership; operator routes (`/admin/v1/*`) compare against the operator token in constant time, which a session never equals, so **a session was never an operator** (phase 38 as built). Since [phase 41](phase-41-signin-methods.md) the session of a person whose verified address is in `QUOTIFY_OPERATOR_EMAILS` is accepted on the operator routes; every other session still is not, and answers 403.

### Threat model

| Threat | Stance |
|---|---|
| Database or backup leak | Passwords are argon2id; session tokens are stored as SHA-256 of 256 random bits. Neither can be turned into a working credential. API keys were already hash-only. |
| Guessing a password | Per-user lock (5 / 15 min), per-address limit at the gateway, argon2id cost, hash concurrency cap. No composition rules, length ≥ 10. |
| User enumeration | Sign-in is uniform in body and time. Sign-up reveals a taken address (no mail to the owner to hide it behind): accepted and limited per address; revisit with e-mail verification. |
| Stolen session token | It is a bearer credential: sign-out revokes it at once, it expires, and the user's other sessions are listed nowhere yet (a follow-up: "sign out everywhere"). Tokens are never in URLs. |
| CSRF | **Not applicable.** Authentication is a bearer token in the `Authorization` header that the app adds from JavaScript; the browser attaches no ambient credential (no cookie), so another site cannot cause an authenticated request. If sessions ever move to cookies this section must be redone (SameSite, CSRF tokens). |
| XSS | The impact is larger than for the operator console because the token is readable by script. Stance: the app is a Vue SPA that renders text with escaping (no `v-html`, no HTML from the API); no third-party scripts are loaded. No `Content-Security-Policy` header is set yet by the console's nginx: adding one (`default-src 'self'`) is a recommended hardening step. The session token is kept in **`sessionStorage`** (dies with the tab, not shared across tabs) rather than `localStorage`. A stricter step — an `HttpOnly; Secure; SameSite=Strict` cookie set by the gateway — trades that for the CSRF section above and is the documented next step. |
| Cross-account access | Authorisation is in SQL (membership join) as well as in the gateway; negative tests cover every route (user A cannot read or act on account B's app, key or tenant; expired or revoked sessions; viewers cannot write; sessions cannot call operator routes). |
| Privilege through the body | Product handlers decode into fixed structs; a `user_id`/`account_id` in a body is ignored (tested). |
| Memory exhaustion by hashing | Four concurrent hashes per replica; password length cap; hash parameter cap on verify. |
| Secrets in caches/logs | Answers that carry a secret (`/auth/*`, a new API key) are `Cache-Control: no-store`. No secret is logged. |

### The overview

`GET /app/v1/apps/{id}/overview?environment=live|sandbox` composes one environment's status from the services that own the facts, calling them **as that environment's tenant** (so every number is the tenant's own; the tenant ids come from plans-svc's membership-checked answer, never from the request):

- **payments** (payments-worker's ledger, newest 1000 rows, last 30 days in the tenant's time zone): payments by status (paid, processing, failed, refunded), totals per currency, a daily series of paid money per currency (the sparkline), the newest payments. A charge that was pending and then succeeded is one payment (rows are folded by provider reference; the most settled outcome wins). `truncated` says when the window held more rows than were read.
- **invoices** (billing): open and paid counts (capped at 100, `more`), the revenue report summed per currency across periods, the newest invoices.
- **customers** (plans, capped at 500), **subscriptions** (the month's recurring revenue per currency), **health** (webhook endpoints, failed deliveries, provider accounts, whether payments-worker answered).

Money is **never summed across currencies**: every amount sits under its currency. A section whose service did not answer is `null` and named in `unavailable`, and the overall `status` is `degraded`; `attention` means a failed payment or webhook delivery; otherwise `ok`. The sections run in parallel with a 6 second budget.

## Verification

- `services/plans-svc/internal/itest/identity_integration_test.go` (real Postgres): sign-up validation and duplicates, hash-only storage, session resolve / slide / expiry / sign-out, uniform sign-in failure, lock and its expiry, app and key lifecycle, **cross-account negative tests for every account-scoped RPC**, viewer restrictions.
- `services/gateway/internal/httpapi/product_test.go`: session required on every product route (API key, operator token, unknown, expired, signed-out), sessions cannot call operator routes, Bob cannot reach Ada's app / overview / keys / tenant, bodies cannot name the user, a session acts on its own tenants only (viewer read-only), per-address rate limits and the forwarded-for rule, the overview's numbers (folding, windows, per-currency totals, degradation).
- Unit tests for the credential package (hash format, constant-time verify, malformed hashes, token shape).
- Not verified: behaviour under real concurrency of hashing (the semaphore is simple and tested by use, not by a load run); nothing here was run on Kubernetes or Railway.

## The web app (second pull request)

Same Vite app (`web/admin`), new lazy-loaded route group; one bundle pipeline. The sign-in page, the dashboard and the settings are separate chunks (the dashboard is about 24 kB, 7 kB gzipped); no chart library: the donut and the sparkline are inline SVG (`shared/ui/donut`, `shared/ui/sparkline`).

**Routes.** `/` and `/signin` (people), `/signup`, `/app` (organisation home), `/app/apps/{id}?env=sandbox` (dashboard), `/app/settings`, `/app/t/{tenant}/{customers|payment-links|credits|webhooks}` (the console's own pages reused), and `/operator` (the operator's token page, moved from `/`). A person with no session is sent to `/signin`; a 401 from the gateway while signed in clears the session and goes to `/signin?expired=1` with an explanation.

**Reuse.** The deeper views are the existing feature pages mounted a second time under `/app/t/$id`. They speak the tenant API through `tenantApi(id)`, which sends the bearer token the tab holds (the operator's or the person's) and `X-Quotify-Tenant`; the gateway lets a session through for its own tenants only. Pages that call operator-only endpoints (invoices, payments ledger, packages, reports) are *not* offered to people; recent invoices and payments appear on the dashboard from the overview instead. A full invoice page against the tenant API is a follow-up.

**Design system** (tokens in `src/app/styles.css`, used by the operator console too): cool light-grey surface, 20 to 24 px white cards with a soft shadow, a dark rounded icon-only rail (a bottom bar on a phone), pastel blue `#8bb8ef` (`--brand`) for charts and highlights and a deeper blue (`--primary`) for actions, soft status pills (paid, open, failed, processing, refunded, healthy, attention) that always carry a word and keep 4.5:1 text contrast, dark mode (system, or chosen with the toggle; stored in `localStorage` as a preference only), `prefers-reduced-motion` respected, visible focus rings. Six languages with key parity; amounts formatted from minor-unit strings without floats and always under their own currency.

**Token storage.** The session token is kept in `sessionStorage` (this tab only, cleared at sign-out); never in `localStorage`, never in a URL.

**Verification.** Playwright specs against a mocked gateway (`e2e-mock/product.spec.ts`): sign up with the exact request body, create the first app, wrong and right passwords, show/hide password, expired session, route guards, app card numbers, dashboard numbers per currency, sandbox switch, degraded section, API key shown once and copied and revoked, reused customers page carrying the session and tenant headers, settings and sign-out, dark mode, phone layout without sideways scroll. The existing mocked specs pass unchanged apart from the operator's new `/operator` entry. Screenshots at desktop and phone width, light and dark, were reviewed while building.

**Not verified:** the app against a real gateway and plans-svc end to end (only the mocked contract, which is generated from the same OpenAPI file); Safari and Firefox; screen readers beyond the accessible names and roles the tests assert; Arabic layout was checked by logic (logical properties, `rtl:` flips), not by eye.
