# 14 — Tenant management: freeze, ban

**Status (2026-11-02): built, with the defaults below (not agreed), including a mail to the account owners on every change, a banner for the owners in the console and filters on the operator's history. Open: the first charge of an invoice finalized before the hold (documented in [13](13-customer-ban.md#the-first-charge-of-an-invoice-finalized-before-a-hold-plan-14-not-built)), appeals, retention before deletion, an account or person ban, operator roles, scheduled suspension.**

## The request
List 2 (10): tenant management with **ban, freeze** and similar operations.

## Where we are
Tenant `status` is `inactive`, `active`, `suspended`. The API `PUT /admin/v1/tenants/{id}/status` suspends or reinstates (a suspended tenant's keys stop at once: `gateway.auth.tenant_not_active`); activating an inactive tenant needs a reason and is logged. #252 added suspend/reinstate to the operator's tenant list (row action, badge, bulk with a result report).

## Gaps
- Only suspend/reinstate. No **ban** (permanent, with data retention rules), no **freeze** that keeps the console readable but blocks writes (read-only mode), no **billing hold**, no scheduled suspension (e.g. unpaid platform invoice), no reason categories, no customer-facing explanation or mail to the account owner, no appeal, no audit log view (only the API), no per-environment behaviour (sandbox vs live) decided.
- Suspension does not stop background work: renewals, dunning, webhooks deliveries, scheduled mails still run for a suspended tenant? (not verified).
- The account (organisation) level: banning a person or an account (not a single tenant) is not modelled; SaaS plan enforcement interplay (Phase 55) unchecked.
- Operator permissions: all operators can do everything; no two-person rule for destructive actions.

## Research first
1. Trace what runs per tenant in the background (renewal, dunning, outbox, webhooks, mailer, usage ingestion, analytics) and what each should do in suspended/frozen/banned.
2. Decide the states and transitions diagram with the owner.

## Decisions needed
State model (suspended, frozen = read-only, banned), effects on data and money, retention before deletion, mail to the owner, whether account/person bans exist, operator roles.

## Work
1. State machine spec + audit schema.
2. Enforce in gateway, workers and billing; tests per component.
3. Console: operator pages for state changes with reason and history; account owner sees a clear notice.
4. Mail to the owner; role-based operator permissions if wanted.

## Done means
Each state has an enforcement test across API, workers and console, and an audit entry; a suspended tenant produces no new charges or mails.

## Risks
A mistaken ban of a paying tenant: confirmations, audit, fast reinstatement.

## Review addendum (2026-11-01)
- **Verified:** only the billing scheduler honours suspension (`billing-svc/internal/rating/inputs/inputs.go`). Saga dunning charges, notifier consumers, webhook delivery, hosted pages and payment links do **not** check tenant status, so a suspended tenant's hosted pages keep taking payments. Add `pkg/tenantinfo` status as step 1.
- `SetStatus` only writes a slog line; the reason is not persisted and `go_live_audit.action` allows two values. Only live tenants have a status; sandbox is always active (`sandbox_is_always_active`).
- Operators are `PAYRUDDER_OPERATOR_EMAILS` sessions or the machine token, and the acting e-mail is available, so a two-person rule is buildable (default off, on by flag; marked default).
- Default statuses (not agreed): `active`, `suspended`, `frozen` (read-only, no new charges), `banned`.

## Done (2026-11-01, branch `feat/block-and-tenant-status`)

Defaults the implementer chose (**default, not agreed**):

1. **Statuses:** `inactive`, `active`, `suspended`, **`frozen`**, **`banned`**. The `tenants.status` CHECK was relaxed to list them (`migrations/00005_block_and_tenant_status.sql`): every existing row satisfies the new CHECK, so the change is safe; the table is small and the lock is brief. **Rollout:** a release that predates it reads `frozen` or `banned` as an unknown status, which the gateway, billing and the notifier count as **active** (so that a rollout in either order does not stop billing). Roll plans-svc out first, and do not freeze or ban during the rollout.
2. **What each does.**
   - **suspended**: API keys stop at once (`403 gateway.auth.tenant_not_active`); the console of the owner is read-only.
   - **frozen**: API keys and the console **still read, nothing is written** (`403 gateway.auth.tenant_read_only` on any call that is not GET or HEAD, including usage ingestion; a frozen tenant's ingest will fail until it is reinstated).
   - **banned**: a suspension meant to last. A ban, and lifting one, needs a **reason**; where `PAYRUDDER_TENANT_BAN_APPROVAL=true` a ban needs a **second operator**.
   - In all three: no payment on hosted pages or payment links, no dunning charge, no customer mail, no webhook leaves (deliveries wait; they go out when the tenant is back, within the 30 days events are kept). Reinstating is `PUT /admin/v1/tenants/{id}/status {"status":"active"}`; moving between the three is allowed.
3. **Reasons and history.** `tenant_status_events` (plans-svc pooled database, append-only by privilege like `go_live_audit`; it moves to `audit_events` of plan 22 (G4)): kind (`changed` or `requested`), from, to, category (`nonpayment`, `fraud`, `abuse`, `legal`, `request`, `other`), reason (500 characters), actor, second operator. `GET /admin/v1/tenants/{id}/status-events` returns the latest 100; the operator's tenant page shows them. The actor is the signed-in operator's address, or the machine token's `X-Payrudder-Actor` header, or `operator`. Only a ban, lifting a ban, and activating an inactive tenant **require** a reason; suspending, freezing and reinstating take one and keep it.
4. **Two-person approval** (`PAYRUDDER_TENANT_BAN_APPROVAL`, **default off**): only a **ban** needs it. The first request is recorded as `requested` and answered `409 plans.tenant.approval_pending`; the same request from **another** operator within 24 hours applies it, and the history names both. The machine token is one actor (`operator`) unless a script sends `X-Payrudder-Actor`, so by default it cannot approve its own request; two scripts that send different headers can approve each other (the header is the token holder's word, as for every operator action).
5. `pkg/tenantinfo.Settings` carries the status (and `OnHold()`), cached like the time zone, so a worker learns a change within the cache TTL (one minute; the notifier uses `NOTIFIER_MAIL_SWITCH_TTL`). Hosted pages and payment links read the tenant on every request.

### What stops where

| Component | suspended, frozen, banned | Test |
|---|---|---|
| gateway, API key | suspended and banned: refused (`tenant_not_active`); frozen: read-only (`tenant_read_only`) | `auth` `TestFrozenTenantKeyIsReadOnlyAndBannedStops`; `httpapi` `TestAFrozenTenantsKeyIsReadOnly` |
| gateway, console session or operator acting as the tenant | read-only in all three | `httpapi` `TestActingAsAFrozenTenantIsReadOnly` |
| hosted pages, payment links, portal purchases | take no payment, make no link, hide the buttons (`billing.tenant.on_hold`) | billing `TheGateRefusesPayments`; gateway `TestGateRefusalsSayThePaymentCannotBeMade` |
| billing scheduler (new invoices) | skipped | `TestBillableFollowsTheTenantStatus` |
| saga dunning charges | put off one hour, not given up | billing `DunningWaitsForATenantOnHold` |
| notifier, every customer mail | dropped (counted in `notifier_mail_dropped_total{category="tenant_on_hold"}`), the saga goes on | notifier `TestPermittedRefusesATenantOnHold`, `TestNotificationOfATenantOnHoldIsSuppressed` |
| webhook dispatch | deliveries wait without using an attempt | plans-svc `TenantStatus` |
| plans-svc, new subscription or plan change | refused (`plans.tenant.on_hold`) | `TenantStatus` |
| status changes | recorded with category, reason, actor; approval for a ban | `TenantStatus`, `TenantStatusApproval` |

### Not built, and what it needs

- **The first charge of an invoice finalized before the hold.** The saga issues it after the PDF; only the dunning retry is held. Holding the first charge needs a hold outcome in the transition table (`sagas/model/table.go`, another change), or a refusal reply from payments-worker that does not start dunning. A tenant frozen between a finalize and its charge is charged once.
- **payments-worker is unchanged.** A provider's confirmation of a payment already started is recorded whatever the status (the money moved).
- **Appeals, retention before deletion** of a banned tenant's data, **account or person bans**, **operator roles**, **scheduled suspension** (for example an unpaid platform invoice) and the **per-environment** question (only live tenants have a status; sandbox is always active). The mail to the owner is built (below).
- **Not verified here:** the console pages in a browser, and the rollout order with an older release.

## Done (2026-11-02, branch `feat/tenant-status-mails`): mail to the account owners

Defaults the implementer chose (**default, not agreed**):

1. **Who is told.** Every owner of the tenant's account whose address is proven (`users.email_verified_at`), in their own language (`users.locale`, six languages, English for any other), not the administrators and viewers, and not an owner whose address is unproven (it may not be theirs). Plans-svc's own mail to people (`identity/usermail`, the sender of verification, invitation and help-desk mail), not the notifier (that one writes to a *tenant's customers* through the tenant's mail switch, and is switched off for a tenant on hold on purpose).
2. **When.** After the change is committed, once per change of status of a live tenant: `suspended`, `frozen` or `banned` say which way the app went (moving between them mails again, with the new word), and `active` after any of them says it is **reinstated**. Activating an inactive tenant is not a reinstatement and sends nothing; neither does a call that leaves the status as it was, nor a ban that is only waiting for the second operator. A mail that cannot be composed or sent is logged and never fails or undoes the change (the sender is bounded and in the background, as for every user mail).
3. **What it says.** The app's name and the account's, what stops (API keys, the console read-only, payments on hosted pages, charges, mail and webhooks; "your data is kept" for suspended and frozen only), and a button to the owner's help page (`{app url}/app/support`, plan 12) to write to the operator, which is also the only appeal there is. **It does not say why**: the category and the reason are the operator's notes (they can name a fraud case or a legal matter) and are not in the mail; a customer-facing reason is a product decision (**default: none**). A ban says that only an operator can lift it, with a reason.
4. **Tests.** `usermail`: every language has the four texts with the same placeholders and lines, the app name is on one line and escaped in the HTML, no placeholder is left. `tenants/command`: which change maps to which mail. plans-svc integration `TenantStatusMails`: suspend, freeze, ban and reinstate each reach both verified owners in the first owner's language naming the app and nobody else (an owner whose address is unproven and an administrator get none), activation and a repeated status send none. The test fails without the call (checked once).

### Not built, and why

- **Retention before deletion and an account or person ban** are not simple: a ban of an account or a person needs a status on `accounts` or `users`, a check in sign-in and in every session and key path (the gateway today reads the *tenant's* status), a rule for what happens to the account's other apps, and a retention period and a deletion job, none of which has an owner's decision. Documented, not started.
- **Not verified here:** the mail in a real mailbox or relay (the tests record what is sent; the texts were not read by a native speaker of each language), and the order of rollout (an older plans-svc simply sends no mail).

## Leftovers built (2026-11-02, branch `feat/product-leftovers-2-p14`)

Defaults the implementer chose (**default, not agreed**):

1. **An owner-visible banner.** Every page of an app in the console (not the organisation's own pages) shows, to every member of the account (owner, administrator and viewer: it is a fact about their app, not a permission), one line per app whose **live** environment is suspended, frozen or banned: what stops, that the data is kept (not for a ban: "only an operator can lift a ban"), and a link to the help page, which is also the appeal. It is shown on the sandbox page of such an app too. **The reason and the category are never shown** (default: none, as in the mail). Six languages (`tenant.hold.*`). The banner reads the status the app list already carries; no API change. The go-live card used to treat a frozen or banned environment as "not active yet, finish these steps" (only suspended said it was held); it now says "On hold" for the three (`product.goLive.suspended`, `stateSuspended` reworded in six languages). Tests: `hold.test.ts` (which statuses count, the scope, the live tenant only, no prototype lookup), mock e2e `tenant-hold.spec.ts` (each of the three, the sandbox page, an active app has none). Looked at in a real Chrome at 375 and 1280 px (English only; Arabic right-to-left not looked at).
2. **Filters on the operator's status history** (the operator's tenant page): where the tenant went, the category and the kind (a change, or a ban waiting for the second operator), several values each, applied on their button like every filter of the console, with a count and a line for "nothing matches". They narrow the latest 100 changes in the browser: the API takes no filter (a tenant has few status changes; paging and server filters are added when one has hundreds). Test: mock e2e `tenant-status.spec.ts`.
3. **Not built, unchanged:** appeals as a form (the help page is the appeal), retention before deletion, account or person bans, operator roles, scheduled suspension, per-environment status.
