# 14 — Tenant management: freeze, ban

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
