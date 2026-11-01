# 14 — Tenant management: freeze, ban

**Status (2026-11-01): built, with the defaults below (not agreed). Open: the first charge of an invoice finalized before the hold, a mail to the account owner, appeals, retention before deletion, an account or person ban, operator roles, scheduled suspension.**

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
- **A mail to the account owner** when their tenant is put on hold (needs plans 11/12 for the sender and the template), **appeals**, **retention before deletion** of a banned tenant's data, **account or person bans**, **operator roles**, **scheduled suspension** (for example an unpaid platform invoice) and the **per-environment** question (only live tenants have a status; sandbox is always active).
- **Not verified here:** the console pages in a browser, and the rollout order with an older release.
