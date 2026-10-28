# Work plans

The owner's two lists of 2026-10-28 (26 items, grouped here into 20 plans), put in the order they should be done, with what each one needs first. **No plan here is started until it has been read and agreed.** The older, shorter record of what was shipped is [`../roadmap-owner-list.md`](../roadmap-owner-list.md).

## Why these plans exist

The first pass went too fast. Twenty-odd changes were merged in a day, each checked only against its own tests, some without lint, none with GitHub CI read afterwards, several against assumptions about outside services that were never confirmed. The result is a main branch that is **red** and features that exist but are thinner than they were described. These plans correct that: research first, a design that the owner can read, small steps, and a definition of "done" that includes what was *not* verified.

## Working agreement

1. **One plan at a time**, in the order below. A plan is finished (code merged, CI green, docs written, loose ends listed) before the next is opened.
2. **Research before design, design before code.** Each plan starts with the sources to read (official provider docs, the code that exists, the owner's decisions). The output of that step is written into the plan and shown before any code.
3. **Before every merge:** the repository's own lint and tests run locally in the same form CI runs them (`make lint`, `go test`, `golangci-lint`, `php-cs-fixer --dry-run`, `npm run lint && typecheck && test`, the mock e2e of the touched pages), and **GitHub CI is read after the merge** (not assumed). A red check is fixed before anything else is started.
4. **Say what was not verified.** Anything that needs a real provider account, a real mail, a cluster or a person's eyes is listed as such in the PR and in the plan, never described as done.
5. **No invented contracts.** A provider's API is read from its current documentation (and the date noted); where the documentation does not settle a behaviour, it is marked UNVERIFIED in the code and in the plan.
6. **The owner decides** what is a product decision (scope, wording of a policy, which providers, which regions). The plans list those as "Decisions needed" and stop there.

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

The mapping of the owner's items to plans is in [the last section](#where-each-of-the-owners-items-went).

## Where each of the owner's items went

**List 1** (the "kalan eksikler" list): 1 support → 12 · 2 Nafru default mail → 11 · 3 more PSPs and e-invoice integrators → 08, 09 · 4 one integrations page → 10 · 5 skippable onboarding → 03 · 6 the welcome tour that keeps returning → 03 · 7 usage and meters icons → 03 · 8 exact compatibility with Paddle and iyzico → 06, 07 · 9 scroll jumping behind sheets and the multi-select in a sheet → 03 · 10 customer payment pages → 04 · 11 dynamic language, invoices by country and language → 05 · 12 taxpayer or individual → 05 · 13 sheets that navigate instead of opening on top → 03.

**List 2** (the "ayrıca" list): 1 MCP → 20 · 2 docs → 19 · 3 quick integration → 18 · 4 Paddle without deprecated features → 06 · 5 iyzico → 07 · 6 filters apply on a button → 02 · 7 one select for sort and order → 02 · 8 price increases and notices → 15 · 9 regions and scaling → 17 · 10 tenant freeze and ban → 14 · 11 customer ban → 13 · 12 bulk actions → 02 · 13 backup and Alertmanager → 16.

## Plan template

Every plan has the same parts: **The request** (the owner's words) · **Where we are** (what exists, honestly, with the PRs) · **Gaps** (what is thinner than it should be) · **Research first** (what to read and what to write down before designing) · **Decisions needed** (the owner's) · **Work**, in order · **Done means** (acceptance and how it is verified) · **Not verifiable here** · **Risks**.
