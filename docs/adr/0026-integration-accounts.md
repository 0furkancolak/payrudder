# ADR 0026 — Several accounts per integration, named by a key

- Status: Accepted as a default, **not agreed** with the owner (written 2026-11-02 under the owner's instruction of 2026-11-01 to pick the plan's default and say so). Builds on [ADR 0016](0016-provider-capabilities-and-routing.md) (providers, capabilities, routing) and [ADR 0023](0023-live-and-sandbox-guards.md) (live and sandbox).
- Plan: [10 — One integrations page](../plans/10-integrations-page.md). Revisit when plan 22 G7/G8 (a stable `account_id`) is built.

## Context

The owner wants one place where an app lists what it has connected, adds several of the same integration, picks a default, and says which one applies to which country. Payments already worked that way by a naming convention (#242): an account is named by its **key**, the provider's name alone (`paddle`, the primary account, which is what everything stored before labels already is) or the name, a tilde and a label (`paddle~eu`). The registry looks through the label to the provider that builds the adapter; everything else stores and compares the key.

The plan asked whether to keep that or move to a real `account_id` (uuid, label, provider, environment, settings, is_default) with the provider name kept apart.

## Decision

1. **Keep the key.** No `account_id`, no migration of `payment_ledger`, `provider_objects`, `payment_attempts`, subscriptions or invoices. The payment path is where a mistake loses money, and a migration there cannot be rehearsed on a copy from here. The convention is cheap to keep because the primary account's key is the provider's name and so nothing stored before labels changes.
2. **Extend the same key to e-invoice.** `einvoice_accounts.provider` holds the key (`nilvera`, `nilvera~tr`, `webhook~erp`); the primary key is `(tenant_id, environment, provider)`; one account per environment is the default (`is_default`, a partial unique index). Rows that existed are the primary accounts and become the default. A buyer's country can name the account that issues the document (`einvoice_routing`, one rule per country, deleted with the account it names); every other buyer goes to the default. A document records the key it began with (`einvoice_issuances.provider`), and a repeat or a cancel of it goes through that account whatever routing says now: an integrator may already hold the document, and a second one must never issue a second legal document under one UUID. A legal number is unique **within an account** (it was within the only account there was); a seller who runs two accounts keeps their series apart with the integrators, which Payrudder cannot see across.
3. **E-mail stays one provider per app** (a tenant's own Nafru Email, or Payrudder's built-in sender). The page lists the sender in use as a row; several mail accounts and a default among them are built when the Nafru contract is (plan 11), because mail has nothing to route by country and a second account adds nothing before that. This is OPEN in the plan, not done.
4. **One audit trail.** `provider_account_events` (payments-worker) gains a `kind` (`payment` or `einvoice`) and the actions `default_set` and `routing_changed`, and is read through `GET /app/v1/apps/{id}/integrations/audit`. It is the pattern of `go_live_audit` until plan 22's `audit_events` exists, and moves there then. No setting is ever written to it. The e-mail provider's changes are plans-svc's and are not in it yet.
5. **The console shows accounts, not provider cards.** The cards stay as the place a provider is first connected and explained; the list below them holds every account of every kind with its default and status, and the rules, the "who takes this customer" tester and the audit sit under it.

## The key as a name: who reads `provider`

This is the trace the plan asked for (2026-11-02, from the code). *Key* means the value is stored or compared as written (`paddle~eu`); *base* means it is looked through to the provider (`paddle`).

| Reader | Key or base | Finding |
|---|---|---|
| `pkg/payments` registry, capabilities, parsers (`Build`, `ParseWebhook`) | base | Looks through the label (`accountkey.go`). |
| `pkg/payments/providers.Lookup` (what a provider can charge) | base | Used by routing validation and subscription creation. |
| payments-worker `provider_accounts`, `provider_account_events`, the resolver cache | key | The primary key of an account. |
| payments-worker `payment_attempts`, `payment_ledger`, `provider_objects`, `provider_subscriptions` | key | Written as the key; the ledger's "one fact once" index includes it. A report grouped by provider shows `paddle~eu` apart from `paddle`. |
| payments-worker webhooks `/webhooks/{provider}/{tenant}/{environment}` | key | Only exercised through the parser lookup (base). A real notification to a labelled account's URL has not been run. |
| iyzico webhooks without a tenant in the URL (`/webhooks/{provider}`) | key, **gap** | The tenant is found from the charge's idempotency key, but which of several iyzico accounts holds the secret that signs it is not known: iyzico with several accounts cannot be told apart. |
| bank transfer | base, **single** | Refused as a further account (`payments.account.single_only`): its details are printed on invoices. |
| plans-svc `routing_rules.provider` | key | **Defect found and fixed here:** the column refused the tilde (`CHECK ... ^[a-z][a-z0-9_-]{0,31}$`), so a rule naming a labelled account could not be saved (an internal error). |
| plans-svc `subscriptions.provider`, `provider_migrations.to_provider`, `domain.ValidProviderName` | key | Same defect, same fix (migration `00009_labelled_accounts`): a subscription could not be sold through a labelled account. |
| plans-svc subscription reports (`group_by=provider`) | key | `paddle` and `paddle~eu` are two rows. Deliberate for now: they are two accounts. |
| billing-svc invoices and payment links (`provider` columns, no check) | key | Stored as written. Hosted pages and payment links offer methods through payments-worker's `invoice-methods`, which asks the account list; a labelled account's methods have **not** been run end to end. |
| gateway `go-live/providers`, `go-live/routing`, `integrations/e-invoice*` | key | The account list, the default and the routing speak keys. |
| console (payments list, filters, the account list) | key | Groups by the key; the page shows provider and label separately. |
| SDK enums | key | The routing `provider` is a pattern (`provider(~label)?`), not an enum of two. |

## Consequences

- Nothing on the payment hot path moved. What is left of the real-`account_id` question is listed above (the iyzico webhook, reports by provider, a webhook to a labelled URL); plan 22 G7/G8 needs the stable id and is the moment to pay for it, with a rehearsal on a copy.
- An e-invoice account is not renamed: its key is its identity (to rename, connect the new label, move the default and routing, remove the old).
- The default cannot be removed while others exist (409 `einvoice.account.default_in_use`): the page asks for another default first, so a live document never finds "accounts, but none is the default".
- Not built: organisation-level accounts that several apps use (the page's organisation screen still connects a payment provider on every app one at a time, as before), copying an e-invoice account between apps, routing of e-mail, health checks of e-invoice accounts that run by themselves (a row says what its last test said).
