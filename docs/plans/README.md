# Work plans

The owner's two lists of 2026-10-28 (26 items) and the lists of 2026-11-01 (contracts, code audit), grouped here into 22 plans, put in the order they should be done, with what each one needs first. **On 2026-11-01 the owner authorised the remaining work to run in parallel batches of independent changes (disjoint files, one worktree each, reviewed and merged one by one) and said not to ask: where a plan lists a decision, the implementer picks a sensible default, writes it down as "default (not agreed)" in the plan and the PR, and builds behind it.** The older, shorter record of what was shipped is [`../roadmap-owner-list.md`](../roadmap-owner-list.md).

**Where things stand (2026-11-02).** Most plans now open with a status that says what is built, what is not and what could not be verified (a provider account, a cluster, a real mailbox, a person's eyes); read that line first. The "Size" and "Needs from the owner" columns below are what each plan was written with, not what is left, and the "Why these plans exist" section below is the history that started them.

## Why these plans exist

The first pass went too fast. Twenty-odd changes were merged in a day, each checked only against its own tests, some without lint, none with GitHub CI read afterwards, several against assumptions about outside services that were never confirmed. The result is a main branch that is **red** and features that exist but are thinner than they were described. These plans correct that: research first, a design that the owner can read, small steps, and a definition of "done" that includes what was *not* verified.

## Working agreement

1. **One plan at a time**, in the order below. A plan is finished (code merged, CI green, docs written, loose ends listed) before the next is opened.
2. **Research before design, design before code.** Each plan starts with the sources to read (official provider docs, the code that exists, the owner's decisions). The output of that step is written into the plan and shown before any code.
3. **Before every merge:** the repository's own lint and tests run locally in the same form CI runs them (`make lint`, `go test`, `golangci-lint`, `php-cs-fixer --dry-run`, `npm run lint && typecheck && test`, the mock e2e of the touched pages), and **GitHub CI is read after the merge** (not assumed). A red check is fixed before anything else is started. On `main` one `ci` run and one `stack` run are in progress at most and one waits behind each; the next merge replaces the waiting run (see [plan 01](01-baseline-and-review.md)), so a cancelled run means "replaced by a newer commit", never "green", and the verdict of a burst is the run of its last commit. A pull request runs the cheap `ci` jobs the change can affect; the whole-stack jobs (`stack`) run on a pull request only with the label `ci:full`.
4. **Say what was not verified.** Anything that needs a real provider account, a real mail, a cluster or a person's eyes is listed as such in the PR and in the plan, never described as done.
5. **No invented contracts.** A provider's API is read from its current documentation (and the date noted); where the documentation does not settle a behaviour, it is marked UNVERIFIED in the code and in the plan.
6. **The owner decides** (since 2026-11-01: the implementer proposes a default and marks it; the owner may overturn it) what is a product decision (scope, wording of a policy, which providers, which regions). The plans list those as "Decisions needed" and stop there.

## How a merge is checked

Before a merge the reviewer runs, on the branch and in the same form CI runs them: `make lint` (golangci-lint on every module and `buf lint`, each module reporting `0 issues.`) and `make gen` (the diff stays clean), `go test` of every touched module, the integration tests of the touched services (narrowly, with `-run`, one at a time), for the console `npm run lint`, `vue-tsc --noEmit` and `vitest` and the mock e2e of the touched pages, and for documentation `cd docs-site && npm ci && npm test`. A migration number is looked up again just before the merge (other branches add migrations to the same services) and renumbered if two collide. **GitHub CI is read after the merge**, not assumed: the author of a change says what was not run (anything that needs an account, a cluster or a person's eyes), and a red run on `main` is fixed before the next batch.

## The order

| # | Plan | Why here | Size | Needs from the owner |
|---|---|---|---|---|
| 01 | [Baseline: make main green and review what was merged](01-baseline-and-review.md) | Nothing else can be trusted on a red main | M | – |
| 02 | [Console interaction contract: filters, sort, bulk actions](02-filters-sort-bulk.md) | Touches every list; do it once, completely, before more lists are added | L | confirm the contract |
| 03 | [Sheets, onboarding, tours, icons](03-sheets-onboarding-tours.md) | Same layer as 02 (shell behaviour), fixes reported bugs | M | – |
| 04 | [Customer-facing pages and portal](04-hosted-pages-and-portal.md) | The page customers actually pay on | L | design direction, which pages |
| 05 | [Customer language, taxpayer or individual, documents by country](05-customer-locale-tax-documents.md) | Feeds 04, 09, 15 | L | tax rules per country |
| 06 | [Paddle: full compatibility, nothing deprecated](06-paddle-parity.md) | Payments correctness before more providers | L | a Paddle sandbox |
| 07 | [iyzico: full compatibility](07-iyzico-parity.md) | same | L | an iyzico sandbox |
| 08 | [More payment providers (Stripe verified, others)](08-more-payment-providers.md) | After 06/07 set the bar | L | which providers, sandboxes |
| 09 | [E-invoice integrators](09-einvoice-integrators.md) | needs 05 (buyer type) | L | which integrators, accounts |
| 10 | [One integrations page: several accounts, default, per country](10-integrations-page.md) | needs 06–09 settled | M | – |
| 11 | [Nafru Email as the default sender](11-email-nafru.md) | needs Nafru's real contract | M | Nafru URL, key, contract |
| 12 | [Support system, finished](12-support-system.md) | needs 11 for mail | L | scope (SLA, channels) |
| 13 | [Customer block and ban](13-customer-ban.md) | needs 04, 08 for enforcement | M | policy wording |
| 14 | [Tenant management: freeze, ban](14-tenant-management.md) | operator side | M | policy |
| 15 | [Price increases and notices](15-price-increase.md) | needs 05, 11 | L | legal notice rules |
| 16 | [Backup and alerting, completed](16-backup-alerting.md) | safety net before scale work | M | storage target, alert channel |
| 17 | [Regions and scaling](17-regions-scaling.md) | biggest architecture item; needs decisions | XL | regions, cloud, targets |
| 18 | [Quick integration for apps](18-quick-integration.md) | needs real SDKs behaviour confirmed | M | – |
| 19 | [Documentation site](19-docs-site.md) | after the product settles | M | hosting |
| 20 | [MCP server](20-mcp.md) | after the API settles | M | hosting model |
| 21 | [Contracts: distance sales and other agreements for the seller's customers](21-contracts.md) | independent of the payment path except one acceptance gate | L | kinds, template text policy, retention |
| 22 | [Platform audit and roadmap: financial correctness first](22-platform-audit-and-roadmap.md) | phases 0–1 outrank 09–12, 14, 15, 17–21 | XL | RPO/RTO, backup target, alert receiver |
| 23 | [Staging and production on payrudder.com](23-staging-and-production.md) | the first deploy; production opens sandbox-only | M | Railway, Cloudflare, nafru-geo and Nafru keys, OAuth clients, sandbox keys |

The mapping of the owner's items to plans is in [the last section](#where-each-of-the-owners-items-went).

## Where each of the owner's items went

**List 1** (the "kalan eksikler" list): 1 support → 12 · 2 Nafru default mail → 11 · 3 more PSPs and e-invoice integrators → 08, 09 · 4 one integrations page → 10 · 5 skippable onboarding → 03 · 6 the welcome tour that keeps returning → 03 · 7 usage and meters icons → 03 · 8 exact compatibility with Paddle and iyzico → 06, 07 · 9 scroll jumping behind sheets and the multi-select in a sheet → 03 · 10 customer payment pages → 04 · 11 dynamic language, invoices by country and language → 05 · 12 taxpayer or individual → 05 · 13 sheets that navigate instead of opening on top → 03.

**List 3** (2026-11-01): contracts for the seller's customers → 21 · the code-base audit (financial correctness, payment reliability, production readiness, reconciliation, developer experience) → 22.

**List 2** (the "ayrıca" list): 1 MCP → 20 · 2 docs → 19 · 3 quick integration → 18 · 4 Paddle without deprecated features → 06 · 5 iyzico → 07 · 6 filters apply on a button → 02 · 7 one select for sort and order → 02 · 8 price increases and notices → 15 · 9 regions and scaling → 17 · 10 tenant freeze and ban → 14 · 11 customer ban → 13 · 12 bulk actions → 02 · 13 backup and Alertmanager → 16.

## Plan template

Every plan has the same parts: **The request** (the owner's words) · **Where we are** (what exists, honestly, with the PRs) · **Gaps** (what is thinner than it should be) · **Research first** (what to read and what to write down before designing) · **Decisions needed** (the owner's) · **Work**, in order · **Done means** (acceptance and how it is verified) · **Not verifiable here** · **Risks**.


## Definition of done for any API change (added after review, 2026-11-01)

1. Change the proto, run `make gen`.
2. Add the gateway route and the entry in `api/openapi/v1.yaml` (`services/gateway/internal/httpapi/openapi_test.go` checks route against spec).
3. Regenerate **all four SDKs** (Go, TypeScript, Python, PHP: `make sdk-php-check` plus each SDK's generate/check) and `web/admin/src/shared/api/schema.d.ts` (`npm run gen`). Add the operation to `docs-site` (its test requires one entry per operation).
4. A new error key: `go test ./pkg/errkey` fails until `docs/error-keys.md` lists it; `UPDATE_ERROR_KEYS=1 go test ./pkg/errkey` rewrites the file. Then `errors.<key>` must exist in all six console locales (`web/admin/src/locales/{en,tr,de,fr,es,ar}.json`, nested by the dotted key).
5. A new mail: six-language templates in `services/notifier/internal/templates/files`.
6. A new webhook event: `plans-svc/internal/webhooks/model/model.go`, the four SDK enums, `web/admin/src/features/webhooks/api.ts`, the locales, `docs/16-webhooks.md`.
7. A migration is a **new numbered file** (never edit `00001_init.sql`); a tenant-scoped table needs its twin in `migrations/tenant/` (`TestTenantSchemaMatchesPublic`); tables the sandbox reset clears are listed in `payments-worker/internal/sandboxreset/sandboxreset.go`.
8. One audit mechanism: the `audit_events` table and `pkg/audit` of plan 22 (G4; [how it works and how to add to it](../audit-log-and-two-factor.md)). Plans 10, 12, 13, 14, 15 and 21 record what a person or the operator does with `audit.Record(ctx, tx, ...)` in the transaction of the change, and put what the console must show in the action's `detail`; they do not add a table of their own for it. The older tables (`app_audit`, `go_live_audit`, `provider_account_events`, `customer_block_events`, `tenant_status_events`) stay, and their actions are written to `audit_events` as well.
9. One list of paths, one table per gate. "Every path that starts a payment or grants access" is listed once, in [plan 13's path-by-gate table](13-customer-ban.md#path-by-gate), which is canonical for the payment gate (`pkg/paymentgate`: a blocked customer, a tenant held). [Plan 21's gate matrix](21-contracts.md#the-gate-matrix-every-path-that-starts-a-payment-or-grants-access) is the second table, for the agreement gate (`contracts.Check`, `requireContracts`), over the same paths; plan 04's pages ask both gates and have no table of their own. A new path gets a row in both tables, and each gate lands server-side before its UI.
