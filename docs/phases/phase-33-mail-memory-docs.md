# Phase 33 — Per-tenant mail switch, memory budgets in CI, ADR note

## Scope

- [x] **Plan** (this file).
- [x] **Mail switch.** A tenant turns Quotify's outgoing e-mail on or off per category: `invoice_issued`, `payment_receipts`, `payment_failed`, `usage_alerts`, `system`.
  - [x] `pkg/mailcategory`: the category list and the template-to-category map, shared by plans-svc and notifier.
  - [x] plans-svc: migration `00015_mail_settings.sql` (`tenant_mail_settings`, RLS, the categories that are off), sqlc queries, module `mailsettings` (`command/`, `query/`, `grpc.go`), `GetMailSettings` / `SetMailSettings` on `PlansService`, error key `plans.mail.unknown_category`.
  - [x] notifier: `Mailer.Permitted` asks the switch before a send (command path and the limit-alert consumer); a disabled category is dropped, counted in `notifier_mail_dropped_total{category}` and answered as done (`NotificationSent.suppressed`), never an error or a retry. A lookup failure is an error (retried), never a send against an unreadable setting.
  - [x] gateway `GET/PUT /v1/mail-settings`, OpenAPI, console tab "Mail" in six languages.
  - [x] Tests: unit (category map, merge rule, switch cache, drop path), integration (plans-svc RLS and API, notifier drop next to a send).
- [x] **Memory budgets.** `deploy/compose/memory-budgets.yaml` (budget per service), `tests/membudget` (checker), `make mem-budget`, a step of the CI `e2e` job.
- [x] **ADR 0020 note**: the subscription report is a fixed sqlc query; the sqlguard test's limits.

## Decisions

- **Where the setting lives.** A new tenant-scoped table, not a column of `tenants`: `tenants` is written only by the operator role (`plans_system`), while a tenant changes this itself through its API key, so the table follows the RLS pattern of `routing_rules`. No row means every category is on.
- **Partial update.** `PUT /v1/mail-settings` changes the categories listed and keeps the rest, so the console toggles one box without sending the others.
- **Categories without a template yet.** `invoice_issued` has no template today (the saga mails the paid and the failed outcome only) and `system` has none either; both are accepted and stored so the contract does not change when such a mail is added. Any template outside the four families is `system`.
- **Enforcement point.** Notifier, before the dedup claim: a dropped mail leaves no notification row, so switching a category on later does not resend what was skipped. The saga still gets its reply and moves on.
- **Cache.** The notifier remembers a tenant's switch for `NOTIFIER_MAIL_SWITCH_TTL` (15 s, at most 1024 tenants), so a month close asks plans-svc once per tenant, and a change takes effect within the TTL.
- **Provider rules are untouched.** Whether a provider sends its own receipts (`SendsOwnReceipts`, Phase 26) is decided in the saga when the invoice draft is made; the switch only adds a second reason not to send. Paddle payments still get no Quotify receipt whatever the switch says, and the switch cannot turn a Paddle receipt on.
- **Console.** There is no tenant settings page, only the tenant's tabs, so the control is a new "Mail" tab.
- **Memory budget placement.** The budget check is a step of the existing `e2e` job, not a job of its own: that job already boots the full stack and has just run the e2e scenarios and the console test against it, which is the smoke run; a separate job would boot a second stack (about ten minutes) for a check that takes seconds. The load profile of the nightly would be more demanding but is not needed to catch a service that doubles its footprint.
- **Measure.** `docker stats` memory of the container (resident set in practice), the measure `tests/bench` already uses; the highest of three samples. Budgets are measured plus the larger of 40 % and 16 MiB, rounded up to 8 MiB. The budgets were taken from the CI runner's own measurement in the first run of this PR (4 to 26 MiB per service; the local Docker VM was shared with other work and its stack was not touched), so they are measured on ubuntu amd64, not on a laptop.

## Exit criteria

A tenant that switches payment receipts off receives none and its payment-failed mail still goes out; a Paddle payment sends no Quotify receipt with the switch on. CI fails when a service passes its budget, and a new compose service without a budget fails the unit tests.
