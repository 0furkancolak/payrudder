# The audit log and two-factor sign-in

Plan 22, G4 and G9. What was built, where it lives, how to add to it, and what is not covered. The plan's [Status section](plans/22-platform-audit-and-roadmap.md#status-2026-11-02-branch-feataudit-2fa-g4-g9) has the decisions and the list of what was not verified.

## The audit log

**One mechanism: `pkg/audit`.** A table `audit_events` in the database of every service that records an action (plans_db, billing_db, payments_db), written by `audit.Record(ctx, tx, audit.Event{...})` in the **same transaction as the change it records**: if the entry cannot be written the change does not happen, and if the change rolls back there is no entry. Plans 10, 12, 13, 14, 15 and 21 write to it (rule 8 of the [plans README](plans/README.md)); the older `app_audit`, `go_live_audit`, `provider_account_events`, `customer_block_events` and `tenant_status_events` stay as they are, and the actions they cover are also written to `audit_events`.

A row: `id`, `tenant_id` (NULL for an entry that belongs to no tenant), `account_id` (set for an entry that is an account's and no tenant's: its members), `actor_type` (`user`, `api_key`, `operator`, `system`), `actor_id`, `actor_email`, `action` (`<area>.<what>`, lower case), `target_type`, `target_id`, `summary` (one line, masked), `detail` (a small JSON object), `before_hash` / `after_hash` (SHA-256 of a state, so two entries can be told to have seen the same state without the log holding it), `source_ip`, `request_id`, `created_at`.

**Append-only in the database.** The app and system roles hold `INSERT` (and `SELECT`); a trigger refuses `UPDATE` and `DELETE` for everybody, the table owner included. The one `DELETE` it lets through is the copy of a tenant between the pooled database and a cell (`payrudder.tenant_move`, as for the financial tables). A sandbox reset keeps the table (`Kept` in each service's `sandboxreset`). Row-level security is forced where the database has it (plans_db, billing_db): a tenant's transaction sees its own rows.

**No secret in it.** `audit.Mask` replaces API keys (`qk_`), session and mailed-link tokens (`qs_ qv_ qr_ qi_ qc_`), webhook secrets (`whsec_`), provider keys (`sk_ rk_ pk_`), bearer credentials, passwords in a URL or query string and any 32-character opaque run in the summary and in every string of the detail; a detail key that names a secret (`secret`, `password`, `token`, `api_key`, `code`...) is replaced whole. A hook still must not pass a secret in: it passes names, prefixes and counts (a webhook endpoint's address without its query, a key's 8-character prefix, the names of the settings changed and not their values).

**Who, from where.** The gateway puts the actor on the request context (`Server.auditActor`): the API key, the signed-in person (also when an operator's session acts as a tenant: then the operator), the operator's address or the machine token's `X-Payrudder-Actor`, plus the client address (the same one the sign-in limits use) and the request id. It travels to plans-svc and billing-svc as gRPC metadata (`audit.UnaryClientInterceptor` in `grpcx.NewTenantClient`, `audit.UnaryServerInterceptor` in `app.NewGRPCServer`) and to payments-worker as `X-Payrudder-Audit-*` headers, which the gateway sets itself and never takes from the caller. A job or a call with no actor is `system`. A sign-in names its user explicitly (`Event.By`).

### What is recorded

| Action | Where it is written |
|---|---|
| `api_key.created`, `api_key.revoked`, `api_key.expiring` (the old key of a rotation) | plans-svc `apikeys/command` (the operator, the console and rotation all go through it) |
| `member.invited`, `member.joined`, `member.role_changed`, `member.removed` (account entries) | plans-svc `identity/command/members.go` |
| `plan.created`, `plan.version_published` | plans-svc `plans/command` |
| `webhook_endpoint.created`, `.updated`, `.deleted`, `.secret_rotated` | plans-svc `webhookendpoints/command` |
| `customer.blocked`, `customer.unblocked` | plans-svc `customers/command` (next to `customer_block_events`) |
| `tenant.status_changed`, `tenant.status_requested`, `tenant.settings_updated` (field names only) | plans-svc `tenants/command` (next to `tenant_status_events`) |
| `credit_note.issued` (with `refund` in the detail), `credit_note.refund_retried` | billing-svc `creditnotes/command` |
| `invoice.void`, `invoice.retry` (the operator's saga actions) | billing-svc `sagas/command/operator.go` |
| `provider_account.connected`, `.replaced`, `.removed`, `.default_changed` | payments-worker `store/accounts.go` (next to `provider_account_events`) |
| `two_factor.enabled`, `.disabled`, `.recovery_codes_replaced`, `.recovery_code_used`, `account.two_factor_policy_changed` | plans-svc `identity/command/twofactor.go` |

**Not recorded yet** (each is a hook of a few lines at the command that owns it): price changes scheduled (`changes`), coupon and promotion changes, routing rules and tax rates, dunning settings, mail integration, e-invoice accounts, offline invoice payments and bank-transfer receipts, the operator's payment-ledger actions, the go-live profile and terms (they have `go_live_audit`), an app archived, password changes and resets, sign-ins themselves and failed attempts. A refund's *outcome* (the provider answered) is not an entry; the request is.

### Reading it

`AuditService.ListAuditEvents` (`proto/payrudder/audit/v1`) on plans-svc and billing-svc, and `GET /admin/tenants/{tenant}/audit-events` and `GET /admin/audit-events` on payments-worker (same fields in the query string, same message as JSON). The gateway asks all three for one more entry than the page shows, merges them newest first (keyset on `(created_at, id)`, the token is the position of the last entry) and answers `next_page_token`. A service that does not answer is named in `unavailable_sources` and `incomplete` is true; if none answers it is `503 audit.unavailable`, and an export refuses to leave entries out.

| Route | Who |
|---|---|
| `GET /v1/audit-events`, `GET /v1/audit-events/export` (CSV, the newest 5000 of the filter) | an API key with `audit:read` (admin and custom keys; **not** the `read_only` role), or a signed-in owner or administrator acting as the tenant; a viewer gets `403` |
| `GET /admin/v1/audit-events`, `.../export` | an operator; `tenant_id` and `account_id` narrow it |

Filters: `action` (names or areas like `api_key.*`, comma separated), `actor` (id or e-mail), `target_type`, `target_id`, `from`, `to`, `page_size`, `page_token`. A tenant's page also holds the entries of its **account** that belong to no tenant (its members), read from plans-svc.

**Cells.** A tenant in a cell has its entries written in its own transaction, so in its schema (the twin migrations); entries written on the system pool (API keys, the tenant's status, members, sign-ins) are always in the pooled database. Asking for a tenant reads both. The operator's view *across* tenants reads the pooled databases only: a tenant in a cell is read by naming it.

### Adding a hook

1. In the command that makes the change, inside its transaction: `audit.Record(ctx, tx, audit.Event{TenantID: ..., Action: "area.what", TargetType: ..., TargetID: ..., Summary: ..., Detail: map[string]any{...}})`, and return its error. In plans-svc `store.DB.TxWith` hands the transaction over.
2. A test that makes the change and reads the entry back (`itest`), and one that looks for the secret.
3. If the area is new: its label in the console (`audit.actions` in the locales) and the area in the console filter.

## Two-factor sign-in

TOTP (RFC 6238: HMAC-SHA-1, 6 digits, 30-second steps, one step either side accepted; verified against the RFC's test vectors) and ten single-use recovery codes. WebAuthn is not built.

* **Secret**: 160 random bits, sealed with `pkg/secretbox` (`PAYRUDDER_SECRETS_KEY`), bound to the user; without the key nobody can set it up (`503 identity.two_factor.not_configured`) and a user who has it cannot sign in. The page draws the QR code itself from the `otpauth://` address (the `qrcode-generator` package), never through a third party, and shows the secret as text.
* **Replay**: `user_totp.last_step` is the last step a code was accepted for; accepting is one atomic `UPDATE ... WHERE last_step < step`, so a code works once even when two requests race.
* **Recovery codes**: `xxxxx-xxxxx` from a 31-character alphabet without look-alikes, stored as an HMAC keyed with the server pepper (`PAYRUDDER_OTP_PEPPER`); spent by an atomic `UPDATE`; shown once; replaced as a set.
* **Setup**: `POST /auth/2fa/setup` → secret and address (a pending secret is replaced by the next setup); `POST /auth/2fa/confirm {code}` proves it, turns it on and returns the recovery codes. Turning off, and making new recovery codes, need a current code or a recovery code.
* **Sign-in**: the first step of **every** method (password, mailed code, Google) ends in `openOrChallenge`: a person without it gets a session as before; a person with it gets a `challenge` (`qc_...`, stored as a hash, five minutes, five tries) and **no** session, no user, no memberships. `POST /auth/2fa/verify {challenge, code}` opens the session. Google returns the browser to `/signin/complete#challenge=...` (the fragment, like the session token).
* **Lock and limits**: each try at the second step is counted in its own statement before the code is looked at (so a failure keeps its count) against the account's lock exactly like a wrong password (five in a row lock sign-in for fifteen minutes), against the challenge's five tries, and against the gateway's limit per client address (10 a minute, shared with confirm, disable and new recovery codes). A password that is right and waits for the second step gives its try back, so a person who closes the tab is not locked out. Every failure of the second step is the same `401 identity.two_factor.invalid`; a locked account, a spent challenge and a wrong code are not told apart.
* **Owners**: `PATCH /app/v1/account/security {require_two_factor_for_owners}` (owners only, default **off**, an owner who turns it on must have it). While on, an owner without it signs in but every route except `/auth/*` answers `403 identity.two_factor.setup_required` (also on the tenant API acting as a tenant of the account) until they have set it up; an owner cannot turn it off while it is required (`identity.two_factor.required_by_organisation`).
* **Audit**: `two_factor.enabled`, `.disabled`, `.recovery_codes_replaced`, `.recovery_code_used`, `account.two_factor_policy_changed`.

**Lost device and lost codes**: there is no self-service path. Today support removes the factor in the database (`DELETE FROM user_totp WHERE user_id = ...; DELETE FROM user_recovery_codes WHERE user_id = ...;` as `plans_system`), after checking who is asking by other means; that leaves no audit entry. An operator action that does it and records it is open.

**Not built**: WebAuthn/passkeys, remembering a device, a mail to the person when two-factor sign-in is turned on or off or a recovery code is used, ending the person's other sessions when it is turned on, the operator reset above.
