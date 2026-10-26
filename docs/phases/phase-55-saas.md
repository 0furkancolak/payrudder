# Phase 55 — Payrudder as a SaaS that bills itself

Delete this file in the closing PR.

## Scope

Payrudder sells its own plans to the organisations that sign up, and meters and bills them with its own engine (dogfooding), not with a side system.

1. A **platform tenant** (the operator's own account, created by `make setup`/migration, hidden from tenant lists) owns Payrudder's plans: Free, Pro, Scale (events per month, apps, members, API keys, features), priced in its currency, with quotas using the quota engine (hard/soft/notify).
2. Every organisation is a **customer** of the platform tenant with a subscription (Free by default, trial for paid plans). Platform limits are enforced where the resource is created (apps, members, keys) and on ingest volume (usage events of the organisation are metered into the platform tenant's meters), with clear error keys and upgrade prompts in the console.
3. **Organisation settings → Plan & billing**: current plan, usage against quotas, invoices of the organisation (from the platform tenant), upgrade/downgrade through the same checkout/payment-link machinery, payment method.
4. **Operator view** in Platform: plans editor (the normal package pages on the platform tenant), organisations with plan and usage, manual plan override.
5. Everything configurable; self-hosters can turn it off (`PAYRUDDER_SAAS=off`: no limits, no plan page). Docs: `docs/17-platform-direction.md` section, a new ADR.
