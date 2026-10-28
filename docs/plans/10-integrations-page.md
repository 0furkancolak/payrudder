# 10 — One integrations page: several accounts, default, per country

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
