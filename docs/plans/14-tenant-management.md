# 14 — Tenant management: freeze, ban

**Status (2026-11-03): operator roles built (read-only support, see the last section); status of 2026-11-02: built, with the defaults below (not agreed), including a mail to the account owners on every change, a banner for the owners in the console and filters on the operator's history. Built 2026-11-02: the first charge of an invoice finalized before the hold is held (`PAYMENT_HELD`, [13](13-customer-ban.md#leftovers-built-2026-11-02-branch-featpayments-hold-first-charge-the-held-first-charge)). Built 2026-11-02 (branch `feat/leftovers-4-tenant-lifecycle`): an appeal, a retention deadline for a banned tenant, a scheduled suspension or freeze. Open: an account or person ban, operator roles (there is no role model to build on, see the end).**

## The request
List 2 (10): tenant management with **ban, freeze** and similar operations.

## Where we are
Tenant `status` is one of **five** values: `inactive`, `active`, `suspended`, `frozen` and `banned` (the first version, #252, had `inactive`, `active` and `suspended`; the other two came on 2026-11-01, see "Done" below). The API `PUT /admin/v1/tenants/{id}/status` moves between them (a suspended or banned tenant's keys stop at once: `gateway.auth.tenant_not_active`; a frozen tenant's are read-only: `gateway.auth.tenant_read_only`); activating an inactive tenant, a ban and lifting a ban need a reason and every change is recorded. #252 added suspend/reinstate to the operator's tenant list (row action, badge, bulk with a result report).

## Gaps
- ~~Only suspend/reinstate. No ban, no freeze, no reason categories, no mail to the account owner, no audit log view.~~ Built (the sections below): `frozen` (read-only) and `banned` (a suspension meant to last, with an optional second operator), reason categories and a history, a mail to the owners, a banner in the console, filters on the history. **Still not there:** retention rules before deleting a banned tenant's data, a billing hold as a state of its own, scheduled suspension (e.g. an unpaid platform invoice), an appeal as a form (the help page is the appeal), and per-environment behaviour (only live tenants have a status; sandbox is always active).
- ~~Suspension does not stop background work: renewals, dunning, webhooks deliveries, scheduled mails still run for a suspended tenant? (not verified).~~ Traced and enforced, see "What stops where" below; the one thing that still runs is the first charge of an invoice finalized before the hold.
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

- ~~**The first charge of an invoice finalized before the hold.**~~ Built 2026-11-02: the saga asks the gate before it sends the charge and rests in `PAYMENT_HELD` (no provider call, no dunning attempt); the dunning sweep charges it, as the first charge, when the tenant is back. A tenant frozen between a finalize and its charge is no longer charged.
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

## Leftover built (2026-11-02, branch `feat/payments-hold-first-charge`): the first charge is held

See [13](13-customer-ban.md#leftovers-built-2026-11-02-branch-featpayments-hold-first-charge-the-held-first-charge): `PAYMENT_HELD`, the one gate (`Engine.Config.Gate`), release by the sweep or by Retry, end by Void. In "What stops where", the billing scheduler's row gains: **the saga's first charge: held, not sent** (`TheFirstChargeIsHeldByThePaymentGate`). **Not verified here:** a real freeze between a real finalize and its charge on a running stack; the seller's Release and Void buttons came later (see [13](13-customer-ban.md#leftover-built-2026-11-02-branch-featleftovers-4-payments-the-invoices-sheet-says-why-a-first-charge-is-held-and-the-seller-can-release-or-void-it)); an environment on hold gets none, because the gateway refuses every write then.

## Leftovers built (2026-11-02, branch `feat/leftovers-4-tenant-lifecycle`): appeal, retention, scheduled suspension

Defaults the implementer chose (**default, not agreed**; the owner may overturn any of them):

**Appeal.**
1. An **owner** of the account (not an administrator or a viewer: they read the same lines and have no form) states why the operator should lift a suspension, a freeze or a ban of the live environment (`POST /app/v1/apps/{id}/appeal`, up to 2000 characters, refused with `plans.appeal.not_held` for an app that is not held and `plans.appeal.exists` while one is open: one open appeal per tenant, enforced by a partial unique index). Table `tenant_appeals` (pooled, global, migration 00033); `GET /app/v1/apps/{id}/hold` gives any member the status, a scheduled status, the retention date of a banned app and the latest appeal. **It never carries the operator's category or note.**
2. The **operator** sees an app's appeals on its tenant page (`GET /admin/v1/appeals?tenant_id=&status=`, the latest 200) and every tenant's open ones on the list of tenants, and decides one (`POST /admin/v1/appeals/{id}/decision`): **accepted** reinstates the tenant (active) and mails the owners the appeal mail with the operator's answer instead of the mail of the reinstatement (one mail, not two); **rejected** needs an answer (an owner whose appeal is refused is owed a sentence), changes nothing and mails the answer. Both are audit entries (`tenant_appeal.submitted`, `tenant_appeal.decided`; no new audit table). Deciding an appeal twice is `plans.appeal.not_open`. If the reinstatement works and the decision cannot be written, the appeal stays open and deciding it again reinstates nothing more.
3. The mails (`tenant_appeal_accepted`, `tenant_appeal_rejected`, six languages, the operator's answer on one line and escaped) go to the same owners as the status mails: proven addresses only, in their language. **Not built:** a mail or a notice to the operators when an appeal is submitted (they see it on the list of tenants), an appeal for an account or a person ban (those do not exist), a second appeal on the same hold is allowed after a decision.

**Retention.** A **ban records a deadline**: the ban's time plus `PAYRUDDER_BAN_RETENTION_DAYS` (default **90**, 0 records none), in `tenant_lifecycle.retention_until`; leaving the ban clears it, a new ban starts a new one. A job (`tenants/command.RunLifecycle`, every minute, the same loop as the schedule below) **marks the deadline due** (`retention_due_at`) once it has passed and writes the audit entry `tenant.retention_due`. **It deletes nothing, ever**: what happens to a banned tenant's data afterwards is a person's decision outside this code (the deletion itself, the notice to the owner and the legal basis are the open questions below). The operator's tenant page shows the deadline and, once marked, that nothing is deleted automatically; the owners of a banned app see "Your data is kept until {date}". There is no way to extend a deadline yet (change the status and ban again, or ask for one).

**Scheduled suspension.** The operator schedules a **suspension or a freeze** (never a ban: it needs a reason and a second operator) of an **active live** tenant for a future time within a year (`PUT /admin/v1/tenants/{id}/scheduled-status {status, schedule_time, category, reason}`, `DELETE` to cancel; one schedule per tenant, a new one replaces the old). The same loop applies it when it is due, as the operator `system` through the same function as a status change by hand (so the history row, the evicted keys and the mail to the owners are the same), **only to a tenant that is active by then**; a tenant that was held by hand meanwhile is left as it is. Either way the schedule is spent and the audit log says which (`tenant.status_scheduled`, `_cancelled`, `_applied`, `_skipped`). The owners of an app that is still in service are told ahead of time on the pages of that one app ("{app} will be suspended on {date}"); **there is no mail before the date** (default, not agreed). The operator's category and reason are not in what the owners see.

**Console.** Owners: `AppHoldPanel` under each line of the hold banner (retention date, the appeal form for an owner, "sent, no answer yet", the decision with the operator's answer), `ScheduledNotice` for the one active app of a page. Operator: *Retention and schedule* and *Appeals* cards on the tenant page, *Open appeals* on the list of tenants; six locales.

**Operator roles: built afterwards (2026-11-03, branch `feat/operator-roles`), see the section at the end.**

Tests: plans-svc integration `TenantLifecycle` (the deadline starts with a ban, ends when it is left and starts again, the job marks it once and deletes nothing, a schedule is refused for a past time, a ban, a sandbox, is replaced by a newer one, is applied at its time as `system` with the mail and the audit entries, is skipped for a tenant that is not active and spent, and is cancelled; an appeal is refused for an app that is not held, for an administrator and when empty, the hold shows an owner what it shows, a second appeal while one is open is refused, rejecting needs an answer, accepting reinstates with one mail, deciding twice is refused), the `usermail` tests (both mails in six languages, the answer escaped and left out when empty), gateway route tests (the user from the session, the operator's actor, an API key reaches none), the cells tests (both tables are global); console mock e2e `tenant-lifecycle.spec.ts` (8 tests) and `hold.test.ts`. **Not verified here:** a real mailbox and the texts by native speakers, the sweeper running for days on a real deployment (the test moves its clock), the console in a browser by a person (the mock e2e ran in Chrome; Arabic right to left not looked at), and an older plans-svc running during the rollout (it ignores the new tables; roll plans-svc out first, then schedule).

## Operator roles built (2026-11-03, branch `feat/operator-roles`)

Two roles, both **default (not agreed)**:

* `operator_admin`: everything, as before. `PAYRUDDER_OPERATOR_EMAILS` stays the admin list, so no deployment changes behaviour. The machine token (`PAYRUDDER_OPERATOR_TOKEN`) is an admin.
* `operator_support`: read-only. Listed in the new `PAYRUDDER_OPERATOR_SUPPORT_EMAILS` (same rules: a verified address, trimmed, lower case). It may call every `GET` of `/admin/v1` and the `POST`s that only read (today one: `.../subscriptions/{id}/change-preview`); every other call gets `403 gateway.auth.permission_denied` ("a support operator is read-only"), an existing key, so no new error key or locale text.
* **An address on both lists is a support operator** (default, not agreed): the more restrictive role wins, so a copy-paste into the wrong list never widens access.
* One parser, `pkg/operator` (`Parse`, `Roles.Of`), used by the gateway and plans-svc. plans-svc reads both lists only to mail the new-ticket notice to admins and to refuse an admin that is also on the support list; a support operator is not mailed (they cannot answer) and is not an assignable assignee (default, not agreed).
* **Classification:** a `GET`/`HEAD` route reads; every other method of an `AccessOperator` route is listed once in `services/gateway/internal/httpapi/operator_class.go` as read or write. An unlisted route counts as a write at run time (fail closed), and `TestEveryOperatorRouteIsClassed` fails for a route of the table that is missing from the list, for a listed route that no longer exists and for a `GET` in the list. A new `/admin/v1` route therefore needs a line there.
* **Acting as a tenant:** an operator session that calls the tenant API with a tenant named (`sessionActAs`) is marked read-only when the role is support, so the tenant routes refuse its writes with `gateway.auth.tenant_read_only`. A tenant `POST` that only reads (a preview) is therefore also refused for support on that path (default, not agreed).
* `GET /auth/me` and the sign-in answers carry `operator_role` next to `operator` (openapi `SessionInfo`/`SignInResponse`, `schema.d.ts` regenerated).
* **Console:** `useCanWrite()` (outside the product's tenant layout, i.e. in the operator console) is false for a support operator; the status, lifecycle, appeal, price pause/notice, bulk suspend/reinstate, dead-letter redrive, organisation plan/2FA reset and support inbox (merge, holidays, canned replies, reply, bulk, status, assignee) controls are disabled with it, on top of the pages that already used it (blocklist, customers, subscriptions, ...). The user menu names the role (`shell.operatorRole.*`, six locales).
* **Audit:** the actor of an operator action is the operator's address, as before. The audit actor has no field for the role and `audit_events` has no column for it: naming the role there needs a migration, so it was **skipped**. A support operator cannot write, so every audited operator action is an admin's.
* Deploy: `PAYRUDDER_OPERATOR_SUPPORT_EMAILS` next to `PAYRUDDER_OPERATOR_EMAILS` in both service `config.default.yaml`, compose (gateway and plans-svc), Helm (`auth.operatorSupportEmails`, gateway and plans-svc), Railway (`OPERATOR_SUPPORT_EMAILS` in `bootstrap.sh`, README table).

Tests: `pkg/operator` (both lists, case and spaces, overlap, unverified, empty); gateway `operator_roles_test.go` (route-table completeness; a support operator reads five GET routes and a read-classified POST, is refused six writes with 403, the same writes pass for the machine token and an admin, `/auth/me` names the role, an address on both lists cannot write); vitest `access.test.ts`; console mock e2e `operator-roles.spec.ts`.

**Not verified:** the Helm render (CI only), compose/Railway on a real host, a real support operator signing in through a mailbox, the console pages not listed above (many operator pages, e.g. providers, e-invoice, seller address and the accounting sheets, still show write buttons to a support operator; the server refuses the call, the page shows the error), the audit trail naming the role (skipped, needs a migration), and a plans-svc older than this change during the rollout (it ignores the new variable and mails every admin, which is the same behaviour as before).
