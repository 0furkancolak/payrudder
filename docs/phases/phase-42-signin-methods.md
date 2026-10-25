# Phase 42 — Sign-in methods: password, one-time code, Google; the operator is a person

## Goal

The owner's requirement: **people sign in with e-mail and password, a one-time code, or Google, and with nothing else.** Until now there was a fourth way, the operator's token typed into a page at `/operator`. This phase adds the two missing methods, removes the token form, and makes an operator an ordinary person whom the server recognises.

## Scope

**In:**

1. **One-time code** (OTP, passwordless): `POST /auth/otp/request` and `/auth/otp/verify`. A code mailed to the address; entering it signs in, and for an address without an account it also creates the user and an organisation (that is the "sign up with a code"). It proves the mailbox, so the address becomes verified.
2. **Google**: OAuth2/OpenID Connect authorization-code flow with PKCE, state and nonce, at `GET /auth/google/start` and `/auth/google/callback`; a new table `user_identities (provider, subject)`.
3. **`GET /auth/methods`** tells the page which methods are on (`password`, `otp`, `google`); the Google button is hidden when it is not configured.
4. **Operators are people.** `PAYRUDDER_OPERATOR_EMAILS` (gateway, comma list, empty by default) names them. The gateway decides on every request, from the session. `PAYRUDDER_OPERATOR_TOKEN` is kept only as the machine credential.
5. **The sign-in page** offers the three methods (and the sign-up page the same); the token form and every other sign-in path are removed.
6. A **stand-in for Google** (`pkg/oidctest`, served by `mockoidc` in the `oidc` compose profile) so the whole flow is tested without Google.

**Out:**

- Multi-factor authentication on top of a method, SMS codes, magic links, other providers (Microsoft, Apple, GitHub: the `user_identities` table and `SignInWithIdentity` are provider-shaped, only the gateway's endpoints are Google's), unlinking a provider, a list of the person's sign-in methods in the settings.
- **Real Google was not exercised.** Every test uses the stand-in. See "Not verified".

## Design

### One-time codes

| Rule | Value |
|---|---|
| Code | 6 decimal digits from the operating system's random source (`crypto/rand.Int`, uniform below 10^6, leading zeros kept) |
| At rest | `login_codes.code_hash` = HMAC-SHA256 keyed with the server pepper `PAYRUDDER_OTP_PEPPER` over `salt ‖ 0 ‖ address ‖ 0 ‖ code`; a 16-byte random salt per code. A code has only a million values, so a plain hash would fall to a leaked table in a moment; the pepper is in the service's environment, not in the database. Without a pepper (development) the salt is all there is: set it in production (compose, Helm and Railway generate/set one) |
| Lifetime / use | 10 minutes; single use (`UPDATE … SET used_at = now() WHERE used_at IS NULL AND expires_at > now() RETURNING`, so of two parallel requests with the right code exactly one wins) |
| Tries | 5 per code. The attempt is counted by the same statement that returns the hash (`TryLoginCode`), with the limit re-checked on the locked row, so parallel guesses cannot exceed it (tested with 20 at once); the fifth wrong try burns the code |
| Sending | One code a minute and five an hour per address (plans-svc, in the database, so it holds across replicas); a new code replaces older ones. A throttled request is answered like any other |
| Per client address | 5 requests and 10 tries a minute at the gateway (`otp/request`, `otp/verify`), counted like the other public routes (`PAYRUDDER_TRUST_FORWARDED_FOR` rules) |
| Comparison | `subtle.ConstantTimeCompare` of the two HMACs; with no live code the same work is done against a dummy, so the time does not tell a never-sent code from a wrong one |
| Errors | One refusal for wrong, expired, used, burned, never-sent and malformed: `400 identity.otp.invalid` |

**No enumeration.** `otp/request` answers `202` for every well-formed address, whether or not it has an account, and a mail with a code goes to every one of them (a code for an unknown address will create the account). The page therefore says "if this address can receive mail, a code is on its way". `otp/verify` fails identically for known and unknown addresses (tested: same code, same message). A malformed address is refused (400), which depends on the input alone.

**Sign-up with a code.** I chose: an address without an account gets one when its code is verified (user, organisation named by the optional `account_name`, else the part before the `@`, owner membership, address verified, **no password**). That makes "Email code" on the sign-up page and on the sign-in page the same operation, and avoids a separate "does this address exist?" question. The cost is that a visitor who types an address and a code they received from their own mailbox gets an account; there is nothing to protect there, since owning the mailbox is the whole claim.

**Accounts without a password.** `users.password_hash` is the empty string for a person created by a code or by Google. `SignIn` answers such an account like a wrong password (same error, same time: it burns an argon2 verify). They can set a password through "forgot password", which proves the same mailbox.

**Squatters.** Somebody can register `victim@example.com` with a password of their own before the victim does (sign-up does not verify the address). If the victim later signs in with a code (or Google), the mailbox is proven for the first time. The account is then *claimed*: **its password is cleared and every session of it is revoked**, so the person who registered it first is out and only the mailbox owner is in (the same outcome a password reset has, by design). A verified account keeps its password and sessions. Tested.

### Google

Gateway only (the browser's redirect and the token exchange are HTTP); plans-svc is told who signed in through `SignInWithIdentity` and trusts the gateway as it trusts it for every other call.

1. `GET /auth/google/start` → the gateway makes a random **state**, **nonce** and PKCE **verifier** (S256 challenge), puts the three (and the language) into a cookie `qg_login` — **HttpOnly, SameSite=Lax, Secure when the console is https, 10 minutes, HMAC-signed** — and redirects to Google (`scope=openid email profile`, `prompt=select_account`). The gateway keeps no state of its own, so any replica finishes a login another started. The key of the HMAC is derived from the client secret; the secret itself is in no cookie, URL or log.
2. `GET /auth/google/callback?code&state` → checks, in this order and before it touches a user: the cookie (signature, expiry), `state` against the cookie's (constant time), then exchanges the code at the token endpoint **with the PKCE verifier and the client secret**, then verifies the **ID token**: RS256 signature against the provider's JWKS (`alg: none` and symmetric algorithms refused; unknown `kid` re-reads the key set at most once a minute), `iss` (Google's two spellings), `aud` = this client (and `azp` when there are several audiences), `exp`/`iat` with a minute of skew, `nonce`, and `sub`. Then it requires **`email_verified: true`**; an unverified address is never used, not even to find a user.
3. `SignInWithIdentity(google, sub, email, …)`: the user is found by **`(provider, subject)`** (`user_identities`, unique) — the subject never changes, the address may; otherwise **linked** to the user with the same address (a user who signs in with a password gets Google added, and keeps the password); otherwise a user and an organisation are created. Linking to an address nobody ever verified claims it (see Squatters). Parallel first logins that collide on the unique index are answered with a retryable error.
4. The browser is redirected to `<PAYRUDDER_APP_URL>/signin/complete#token=qs_…`. The token is in the **fragment**: no server, proxy or Referer sees it; the page reads it once, removes it from the address bar and keeps it in `sessionStorage` like any other sign-in. A refusal is `/signin?oauth_error=<one of state, denied, token, email_unverified, unavailable, failed>`: a fixed vocabulary the page translates, never provider text. Details of why go to the log without any secret.

Settings (gateway; YAML defaults, secrets only from the environment): `PAYRUDDER_APP_URL`, `PAYRUDDER_GOOGLE_CLIENT_ID`, `PAYRUDDER_GOOGLE_CLIENT_SECRET`, `PAYRUDDER_GOOGLE_REDIRECT_URL` (default `<APP_URL>/gw/auth/google/callback`: the console forwards `/gw/*` to the gateway, so the cookie, the callback and the app share one origin), `PAYRUDDER_GOOGLE_ISSUER` (default `https://accounts.google.com`; development points it at the stand-in). Both client settings or neither is a startup error. How to create the Google OAuth client and which redirect URI to register: [docs/13-admin-ui.md](../13-admin-ui.md#sign-in-methods).

The **button** follows Google's branding rules: the unchanged four-colour "G" on a white (light) or `#131314` (dark) face with Google's grey outline, at least 40 px high, the label "Continue with Google" translated into the page's language, nothing added to the mark. It is a plain link to the gateway, not a script.

### Operators

- **Evaluated on the server, on every request** (`operatorAllowed` in the gateway): the request's session token is resolved (the one primary-key lookup every session request makes) and the user's address must be in `PAYRUDDER_OPERATOR_EMAILS` (trimmed, lower case, constant-time compare) **and verified**. A person who signs in by code or Google has a verified address by construction; a password person must have followed the verification link. The flag is not stored on the user and no client value feeds it: headers (`X-Payrudder-Operator`, `X-Operator`, …), body fields (`operator`, `email`) and the browser's own storage are ignored (tested).
- The machine token still works exactly as before on `/admin/v1/*` and as "act as" (`X-Payrudder-Tenant`). An operator's session does the same: it passes the operator routes and may act as any tenant; a person's session still acts only as the tenants of its own apps.
- A **valid session of a non-operator** on an operator route is `403 identity.operator.forbidden` (not 401, so the page does not take it for an expired session); a missing or invalid one is 401; an unreachable plans-svc is 503, never "not an operator".
- The sign-in answers and `/auth/me` carry `operator: true|false`. The page uses it only to show the side-bar door to the console (`/operator`, which opens the tenant list) and the console's pages; `/auth/me` corrects a stale value on every load, and a person who is not an operator who reaches a console URL is sent home.
- The static token is documented as a machine credential in the OpenAPI file, `docs/13-admin-ui.md`, `docs/10-local-dev.md`, the Helm values and the Railway README; `PREFILL_OPERATOR_TOKEN` is gone (nothing secret is served to the browser any more; `PREFILL_PLAYGROUND` pre-fills the playground's form in dev).
- Dev stack: the compose file defaults `PAYRUDDER_OPERATOR_EMAILS` to `user1@example.com` (the dev seed's first user, verified by the seed).

### Threat notes

| Threat | Stance |
|---|---|
| Guessing a code | 10^6 values, 5 tries per code, 5 codes an hour per address (at most 25 guesses an hour per address), per-address limits at the gateway, constant-time compare |
| Leaked `login_codes` table | Keyed HMAC with a pepper kept outside the database: not searchable offline without the pepper |
| Enumeration | `otp/request` is the same for every address; `otp/verify` fails the same; `/auth/methods` is public and says nothing about people |
| Mail flooding | One code a minute and five an hour per address, 5 requests a minute per client address, the bounded background mail sender of phase 39 |
| Login CSRF / code injection in the Google flow | `state` bound to the browser by a signed HttpOnly cookie, PKCE, nonce in the ID token, exact redirect URI |
| Forged or replayed ID token | Signature against the provider's keys (RS256 only), `iss`, `aud`/`azp`, `exp`, `nonce`; an authorization code works once at the provider (the stand-in does the same; replay tested) |
| Unverified provider address | Never used, to find, link or create anybody |
| Account takeover by pre-registering an address | Claiming a mailbox clears the password and revokes the sessions of an account whose address was never verified |
| Session token in the redirect | In the URL fragment, read once and removed; never a query parameter |
| Secrets in logs | The client secret, codes, tokens and the pepper are never logged; refusal reasons are error kinds |
| Privilege through the client | Operator status is the server's verdict per request; claims are ignored; a person's storage edit opens nothing |

## Verification

- `services/plans-svc/internal/itest/signin_integration_test.go` (real Postgres): code format and randomness, stored value is the keyed hash with a 10-minute lifetime, throttling (a minute, five an hour, silently), unknown address creates user + organisation + verified + no password, single use, expiry, five wrong tries burn the code (also with 20 parallel guesses), a new code replaces the old one, malformed input is one refusal, a code of one address does not work for another, no enumeration, a right code spent eight times at once signs in once, squatter claimed (password and sessions die), existing user gets no second account; Google: new person created, subject found even when the address changed, unverified address never used (also for an existing user), linking keeps the password, linking to a never-verified address clears it, `(provider, subject)` is unique, unknown provider refused, provider accounts have no password to guess.
- `services/gateway/internal/httpapi/signin_methods_test.go` (against `pkg/oidctest`, no Google): operator list (listed + verified passes; plain user, unverified, look-alike address, no and invalid session do not; empty list means nobody), spoofed headers and body claims ignored, `/auth/me` verdict, operator session acts as a tenant while a person cannot, code routes and their limits, `/auth/methods`, **Google**: happy path (all authorization-request parameters, no secret in the URL, cookie flags, plans-svc asked with the right claims) and refusals (different state, no / forged / edited / expired cookie, access denied, wrong audience, nonce, issuer, expired token, no subject, several audiences without `azp`, forged signature, unverified address, `email_verified: "false"`, code replay), `alg: none` and HS256 tokens, the cookie never carries the secret. The existing `TestSessionsCannotCallOperatorRoutes` now expects 403.
- Vitest: the six code boxes' logic, the session module (operator only while signed in and only when the server said so; no operator token exists), locale parity and error-key coverage for the new keys in six languages.
- Mocked Playwright (`e2e-mock/signin.spec.ts`): methods shown/hidden, Google link and branding, the return from Google and its error messages, typing / pasting / Backspace / arrows in the code boxes, wrong code, the resend countdown, sign-up with a code, Arabic (page right to left, digits left to right), phone width, the operator signing in like anybody else and the console door, a non-operator without it, and a forged storage claim corrected by the server. All earlier mocked specs sign in as the operator user through the page.
- Real-stack specs (`e2e/*.spec.ts`) hold the session of the operator user (`e2e/operator.ts`: the dev seed's `user1@example.com` and its password if that person exists, else a one-time code read from Mailpit, which also creates and verifies them on a stack without the seed such as CI's) instead of typing a token; `e2e/signin-real.spec.ts` drives the code and Google (through the stand-in, skipped when the gateway has no Google client) methods with real mail; `smoke.spec.ts` also checks that a non-operator gets 403 from the API and is sent home by the page, and that the machine token is no password.

## Not verified

- **Real Google was NOT exercised.** No Google client exists for this repository: the flow was tested end to end against `pkg/oidctest`, which implements the same endpoints and checks (client secret, exact redirect URI, PKCE) but is our own code. Things only Google can show: its exact discovery document and key rotation timing, `prompt=select_account` behaviour, the consent screen, the `hd`/`email_verified` claims for Workspace accounts, and Google's branding review. Follow the setup steps in `docs/13-admin-ui.md` and try it once with a real client before relying on it.
- Delivery of the code mail through a real SMTP relay and how mail clients render it (Mailpit only); the subject carries the code so a lock-screen preview shows it, which is the usual choice for codes.
- Behaviour behind a proxy that is not the console's nginx (the per-address limits depend on `PAYRUDDER_TRUST_FORWARDED_FOR`); Kubernetes and Railway values are rendered (`helm template`, `bootstrap.sh --dry-run`) but were not deployed.
