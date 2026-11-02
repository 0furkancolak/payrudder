# 10 — One integrations page: several accounts, default, per country

**Status (2026-11-02): partly done.** The page lists the accounts of all three kinds, e-invoice and **e-mail** have several accounts with a default and routing (by country for e-invoice, by the language of the mail for e-mail), the audit trail is readable, and a defect that made labelled payment accounts unusable is fixed. Organisation-level accounts (the migration they need is written in [ADR 0026](../adr/0026-integration-accounts.md#organisation-level-accounts-why-not-on-the-key-and-the-migration-it-needs-written-2026-11-02-nothing-built), no code) and a real run against two sandbox accounts are open (see "What is open"). The data-model decision is in [ADR 0026](../adr/0026-integration-accounts.md).

## The request
List 1 (4): one general integrations page where the user can add **several of the same integration**, choose a **default**, **bind one to an app**, and choose **which one applies to which countries**.

## Where we are
- #242: an account is named by a key `provider~label` (`paddle~eu`); several accounts at one provider, a default that can be moved without resending keys, `GET/PUT /app/v1/apps/{id}/go-live/routing` (country, currency → account key), an accounts card on the integrations page of an **app** (list, add under a label, make default, edit routing rules). The registry looks through the label; routing validation accepts keys.
- The page also has cards for payments, e-invoice (Nilvera, webhook) and e-mail (built-in, Nafru).

## Gaps
- The model is a **naming convention on the provider key**, chosen to avoid a migration; it was not designed against the whole requirement. Consequences: webhooks for a labelled account use `/webhooks/{provider~label}/{tenant}/{environment}` (only exercised through the parser lookup); the ledger and `provider_objects` store the key as a provider name (reports and the UI that group by provider will show `paddle~eu` separately); iyzico, which sends webhooks without naming a tenant, cannot tell accounts apart; bank transfer is forced single.
- **Only payments** have several accounts. E-invoice integrators (one per environment) and e-mail (one per tenant) do not; "several of the same integration" applies to them too.
- **"Bind one to an app"**: accounts are per tenant (per app environment) already, but the **organisation-wide** view (one account used by several apps) and copying a connection between apps is the old "connect on all apps" loop, not a shared account.
- **Per country**: routing is (country, currency) → account for payment, done by first match. Not covered: routing for e-invoice integrators by country, fallback when an account is unhealthy, preview of "which account would take a customer in X paying Y" in the console (an API exists: `previewRouting`).
- The labelled accounts have no sandbox/test connection; the sheet cannot edit the label; no audit view in the console for who changed which account.
- Not tested: a real charge routed to a labelled account end to end; hosted pages and payment links with labelled accounts (they list providers by name).

## Research first
1. Trace everything that reads `provider` as a name: ledger, provider_objects, hosted pages (which methods to offer), payment links, reports, reconciliation, webhooks, dunning, provider migrations, console groupings, SDK enums. List each with "key or base provider?".
2. Decide the data model properly: a real `account_id` (uuid, label, provider, environment, settings, is_default) with the provider name kept separately, vs. the key convention. Estimate the migration (payments-worker tables `provider_accounts`, `payment_ledger`, `provider_objects`, plans-svc `routing_rules`, subscriptions' `provider`).
3. Write the **user stories** of the page from the owner's sentence (who sets what, at which level: organisation, app, environment).

## Decisions needed
- Keep the key convention or move to real account ids (cleaner, one migration, touches the payment hot path).
- Scope of "several": payments only, or e-invoice and e-mail too.
- Account scope: per app-environment (today), or organisation-level accounts that apps opt into.

## Work
1. Spec of the page and the model (this file updated) and an ADR.
2. Data model migration if chosen; adapt ledger, webhooks (account id in the URL), reports.
3. Integrations page: one list of **accounts** (not provider cards) with filters, default markers, health, and a unified "Add integration" flow for all kinds; routing editor with a "who would take this customer" tester; audit trail view.
4. Several e-invoice integrators and routing by country (needs 09).
5. Tests: unit (routing), integration (worker with two accounts, webhook per account), e2e (add, default, route, remove, failure modes).

## Done means
Two real sandbox accounts of one provider both take charges by country; webhooks reach the right account; the page covers payments and e-invoice and e-mail.

## Not verifiable here
Real routing across live providers.

## Risks
A data migration on the payment path: needs a rehearsal on a copy and a rollback.

## Review addendum (2026-11-01)
- Plan 22 (G7, G8) needs a stable `account_id`; record that dependency. Reuse `provider_account_events` for the audit view; add a "who takes this customer" tester using `previewRouting`.
- **Status (2026-11-02, console, `feat/console-fee-rules-health`):** the page now shows each payment account's health (what payments-worker last saw, an open circuit first) and sets the failover (owner), with a Health filter; see [`../provider-health.md`](../provider-health.md) and [13-admin-ui](../13-admin-ui.md).
- **Status (2026-11-02, plan 22 G7/G8):** reconciliation (`reconciliation_discrepancies`, `settlement_lines`, `fee_rules`) and the provider health state key their rows by the **account key** (`provider~label`) the ledger already uses, exactly as the convention stands; none of them has an `account_id`. If this plan moves to real account ids, those tables (and `provider_account_health`, plan 22 G8) are two more places of the migration: `provider` becomes the account id and the base provider is looked up. Nothing in either feature reads the label other than through `payments.BaseProvider`.


## What was done (2026-11-02)

**Defaults chosen (not agreed; the owner may overturn them):**
- Data model: **keep the key convention** for payments (`provider~label`), no `account_id` and no migration on the payment path; extend the same key to e-invoice accounts. The trace of every reader of `provider` and what it found is in ADR 0026. The stable `account_id` of plan 22 (G7, G8) is the moment to revisit.
- Scope of "several": payments and **e-invoice**. E-mail stays one own provider per app (open, below).
- Account scope: per app-environment, as today.
- A legal number is unique within an e-invoice account, not across a tenant's accounts (two accounts number on their own).
- The default e-invoice account cannot be removed while others exist; the page asks for another default first.

**Fixed (found by the trace):** a routing rule naming a labelled payment account (`paddle~eu`) could not be saved, and a subscription could not be sold through one: `routing_rules.provider`, `subscriptions.provider` and `provider_migrations.to_provider` refused the tilde (plans-svc migration `00010_labelled_accounts` and its tenant twin `00008`; `TestPlansSvc/LabelledAccounts` fails without it, with an internal error). #242 had never been run against the database.

**Built:**
- payments-worker: several e-invoice accounts per environment (the key in `einvoice_accounts.provider`, a default, `einvoice_routing` by the buyer's country), an issuance records its account and repeats and cancels go through it, `provider_account_events` gains a `kind`, `default_set` and `routing_changed`, and is listed (`GET /admin/tenants/{tenant}/provider-account-events`). Moving the default of a payment account is now recorded too. Migration `00005_integration_accounts` and its tenant twin `00006`.
- gateway/OpenAPI (`/app/v1/apps/{id}/...`): `integrations/e-invoice` lists `accounts` (and `account`, the default), takes a `label` and `default`, `DELETE`/`test` name the account; new `integrations/e-invoice/default`, `integrations/e-invoice/routing` (get, put), `integrations/audit` and `go-live/routing/preview` (the tenant API's routing preview, for the console). These are session routes: the SDKs and the docs site cover `/v1/*` only, so they do not change (generators checked).
- Console: one list of every account (kind, provider, label, mode, status, default), filtered by kind and status and searched; one "Add integration" flow (kind, provider, then that provider's own form); make default, test and remove from a row; payment routing as before and e-invoice routing by country; "Who takes this customer?" (country and currency: the payment account by the server's own preview, the e-invoice account by the worker's rule, the sender in use); and the audit, a page at a time, naming the person. Six languages and three new error keys (`einvoice.account.default_in_use`, `einvoice.routing.invalid`, `einvoice.routing.unknown_account`).

**Tests:** worker unit (account selection by country, a repeat and a cancel keep their account, no default is a missing account), command validation, sealing keeps the old form for the primary account; integration `TestEInvoiceAccountsRouteByCountry` (two integrators, routing, default, refusal to remove the default, cancel through a removed account, paged audit with no setting in it), `TestPaymentAccountDefaultMoveIsAudited`, `TestPlansSvc/LabelledAccounts`, the tenant-schema parity tests; gateway route tests; console unit tests of the list, the e-invoice matching and the audit actor; mock e2e of the list, the second account, routing, remove, tester, audit and the viewer.

## What was done for e-mail (2026-11-02, branch `feat/leftovers-4-integrations`)

**Defaults chosen (not agreed; the owner may overturn them):**
- **Routing is by the language of the mail, not by country.** The notifier has no country of the recipient, but every mail carries `locale` (the customer's, else the app's default, else English). A mail in a language with no rule goes through the default account; a rule dies with its account.
- The same key as the other kinds (`nafru`, `nafru~eu`), one default per app, the first account is the default, the default cannot be removed while others exist (409 `plans.mail_integration.default_in_use`), a label connects a further account, `make_default` moves it.
- A tenant's own accounts are still only Nafru Email; a second provider type waits for plan 11's contract (UNVERIFIED there).

**Built:** plans-svc migration `00027_mail_accounts` (and tenant twin `00019`): `tenant_mail_integrations` PK `(tenant_id, provider)`, `is_default`, `tenant_mail_routing`; the rows that existed become the primary and default account (a column default, because an `UPDATE` matches nothing under forced row-level security); proto `MailIntegrationService` (`accounts`, `label`, `make_default`, `account` on delete and test, `SetDefaultMailIntegration`, `GetMailRouting`, `PutMailRouting`, `ResolveMailSender(language)`); audit entries `mail_integration.saved|removed|default_set|routing_changed` (never a key); the notifier asks for the mail's language and caches per tenant and language; the reseal job and the sealing AAD know the account key (the primary account keeps the old AAD); gateway `integrations/mail` (`PUT` with `label`/`make_default`, `DELETE ?account=`, `test` with `account`), `integrations/mail/default`, `integrations/mail/routing` (get, put); console: every e-mail account in the one list with its default marker, make default, remove, the sheet with a label and a default box, the rules "language to account", and the tester "who takes this customer" with a language. Six languages, two error keys (`plans.mail_integration.default_in_use`, `.unknown_account`), two audit labels.

**Tests:** plans-svc `TestPlansSvc/MailAccounts` (first is the default, labelled beside it, default moves with no key, a rule per language, `ResolveMailSender` follows the language, the default is kept while others exist, a rule dies with its account, another tenant sees none of it, the audit has no key), `TestMailAccountsMigrationKeepsTheOneAccountThereWas` (data of version 26 through 27 and back), the AAD unit test against the reseal job's rule, the tenant-schema parity test; notifier `TestPlansProvidersAskForTheLanguageOfTheMail` and `TestDeliverAsksForTheProviderOfTheMailsLanguage`; gateway `TestMailAccountRoutes`; console unit tests of the list, the legacy shape and the taker, and the mock e2e of adding, default, routing, tester and removal.

## What is open

- **Organisation-wide accounts** (one account used by several apps) and copying a connection between apps: still the old "connect on every app" loop. The data model **does not allow it without the `account_id` move**: the migration plan (six steps, rehearsal, rollback) is written in [ADR 0026](../adr/0026-integration-accounts.md#organisation-level-accounts-why-not-on-the-key-and-the-migration-it-needs-written-2026-11-02-nothing-built) and no code was written for it.
- **E-mail accounts are not in the integrations page's audit card**: their changes are in `audit_events` (the account's audit log page lists them), the card reads payments-worker's `provider_account_events`. Not checked by a real send: two Nafru accounts sending in two languages (needs two Nafru accounts).
- **Not run against anything real:** a charge through a labelled payment account end to end, hosted pages and payment links offering a labelled account's methods, a notification to a labelled account's webhook URL, iyzico with two accounts (its notification names no tenant, so the account that signed it cannot be told), two sandbox accounts of one provider taking charges by country. Nothing here was run against an integrator or a provider.
- A document that an integrator refused for good stays with the account it began with; if the account was the wrong one, moving it needs a reset of its issuance row (not built).
- E-invoice accounts have no standing health check: a row says what its last test said. Routing "fallback when an account is unhealthy" is not built.
- The audit view covers the live environment's accounts; the sandbox's test-mode accounts are another tenant and are not in it.
- Migrations `payments-worker 00003` and `plans-svc 00010` were rehearsed on empty databases (the integration tests and the tenant-schema parity tests), **not on a copy of real data**; the plan's own risk about the payment path stands for the plan 22 `account_id` move, not for this change (no payment table changed).
