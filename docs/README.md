# Payrudder documentation

Status: **built**; what is left is on the [roadmap](phases/ROADMAP.md). These documents are the source of truth for the design. Code that disagrees
with them is a bug in the code or in the docs. Fix one of them in the same PR.

> **Work plans:** the owner's open requests, ordered, each with its gaps, research and acceptance criteria, are in [plans/](plans/README.md). Nothing in them is started until it is agreed.

> **Publication:** [publication.yaml](publication.yaml) says which of these documents are public (the developer site and the MCP server publish only those) and how finished each one is. A new file needs a line there; a test fails until it has one.

## Core

| Doc | What it answers |
|---|---|
| [00-vision.md](00-vision.md) | Why Payrudder exists, who uses it, goals, non-goals, success criteria |
| [01-glossary.md](01-glossary.md) | Shared vocabulary: tenant, customer, meter, event, period, invoice… |
| [02-architecture.md](02-architecture.md) | System diagram, service map, main data flows |
| [04-messaging.md](04-messaging.md) | Every Kafka topic and RabbitMQ exchange/queue |
| [05-data-model.md](05-data-model.md) | Per-service tables, RLS policies, indexes |
| [06-multi-tenancy.md](06-multi-tenancy.md) | Isolation model, API keys, quotas |
| [07-correctness.md](07-correctness.md) | The ten distributed-systems problems and their solutions |
| [08-observability.md](08-observability.md) | Traces, metrics, logs, dashboards, SLOs |
| [09-testing-strategy.md](09-testing-strategy.md) | Unit → chaos; the 1M-event correctness test |
| [10-local-dev.md](10-local-dev.md) | Docker Compose stack, ports, make targets |
| [11-repo-layout.md](11-repo-layout.md) | Planned monorepo structure |
| [12-kubernetes.md](12-kubernetes.md) | Running Payrudder on kind with Helm: charts, profiles, hooks, network policies, what is not covered |
| [deploy/railway/README.md](../deploy/railway/README.md) | Running Payrudder on Railway: services, images, variables, bootstrap script, what is not verified (phase 31) |
| [audit-log-and-two-factor.md](audit-log-and-two-factor.md) | The audit log (one table per service database, written with the change, read through the gateway) and two-factor sign-in (TOTP, recovery codes, the second step of every sign-in method, the owners' requirement) |
| [13-admin-ui.md](13-admin-ui.md) | The operator console: views, the typed API client, tests |
| [17-platform-direction.md](17-platform-direction.md) | Rename-able product, apps and addresses, isolation modes, licence, demo, dev and edge: the requirements and the phases that build them |
| [16-webhooks.md](16-webhooks.md) | Signed outbound events, endpoint management, delivery and retries, the hosted invoice page |
| [19-contracts.md](19-contracts.md) | Agreements for the seller's customers: versions, acceptances with evidence, the hosted boxes and the gate, withdrawal requests, the legal limits |
| [15-billing-lifecycle.md](15-billing-lifecycle.md) | Yearly plans, fees in advance, upgrades and downgrades, the Paddle catalogue and API, credit notes, coupons and tax |
| [14-billing-platform.md](14-billing-platform.md) | Subscriptions, iyzico and Paddle, routing by country and currency, provider migration, reports: the design and what was built |
| [payment-provider-contract.md](payment-provider-contract.md) | What a payment provider adapter must do, the conformance suite it passes, and the compatibility matrices of [Paddle](paddle-compatibility-matrix.md), [iyzico](iyzico-compatibility-matrix.md) and [Stripe](stripe-compatibility-matrix.md) |
| [document-rules-by-country.md](document-rules-by-country.md) | What an invoice and a credit note must carry in the EU, TR, DE, FR, ES, GB, SA, AE and US, how they are numbered, in which language, with the primary source read or not read for every row, and what the PDF prints today and does not |
| [einvoice-integrator-checklist.md](einvoice-integrator-checklist.md) | The eighteen questions to put to an e-invoice integrator before an adapter is written, each tied to the conformance check that proves the answer |
| [einvoice-integrator-contract.md](einvoice-integrator-contract.md) | What an e-invoice integrator adapter must do, the conformance suite and the fake integrators it runs against, and how the generated UBL is checked against the EN 16931, Peppol BIS 3 and UBL-TR rules |
| [18-sdks.md](18-sdks.md) | The client libraries for the tenant API and for checking webhook signatures (TypeScript, Go, Python, PHP): what each does and how it is tested |
| [error-keys.md](error-keys.md) | Every `error.key` the API can send, generated from the code (`go test ./pkg/errkey`); a client translates keys, not messages |
| [reconciliation.md](reconciliation.md) | The review queue of differences between invoices, payments and a provider's own reports, and why "paid" is not "settled" (settlement report, fee rules) |
| [provider-health.md](provider-health.md) | Payment account health, the circuit breaker, and failover that cannot charge twice |
| [hosted-pages.md](hosted-pages.md) | The pages a customer sees without signing in (portal, invoice, payment link, receipt): where each state comes from and what is still missing |
| [email-template-contract.md](email-template-contract.md) | What Payrudder sends as mail, as a contract with a provider that may render it (Nafru Email): the message, the variables, what is not built |
| [geo-data.md](geo-data.md) | Countries, regions and districts of the address forms (GeoNames): licence and attribution |
| [benchmarks.md](benchmarks.md) | Measured numbers, each with the machine and commit it came from |
| [capacity.md](capacity.md) | What the platform has been measured to do, where the first limit was seen, and how to find the cause of one |

## Runbooks

| Runbook | What it covers |
|---|---|
| [backup-restore.md](runbooks/backup-restore.md) | Backup, restore to a chosen minute, the objectives (RPO, RTO) and where alerts go |
| [stateful-components.md](runbooks/stateful-components.md) | One row per thing that holds state: what is backed up, how it comes back, how much can be lost |
| [failover-game-day.md](runbooks/failover-game-day.md) | What to do when a stateful part is gone, and how to prove on a scratch environment that it works |
| [secrets-key-rotation.md](runbooks/secrets-key-rotation.md) | Replacing `PAYRUDDER_SECRETS_KEY` without stopping the platform |
| [cells.md](runbooks/cells.md) | Cells mode (schema per tenant): vocabulary, placing and moving a tenant ([ADR 0018](adr/0018-tenant-isolation-modes.md)) |
| [scaling.md](runbooks/scaling.md) | Regions, connection pooling and autoscaling: all off by default |
| [mail-suppression.md](runbooks/mail-suppression.md) | The per-tenant list of addresses that are not mailed (bounces, complaints) |
| [console-first-deploy.md](runbooks/console-first-deploy.md) | The first deploy of the console on Cloudflare Workers ([ADR 0021](adr/0021-console-on-cloudflare-workers.md)) |

## Service specifications

| Service | Spec |
|---|---|
| gateway | [03-services/gateway.md](03-services/gateway.md) |
| plans-svc | [03-services/plans-svc.md](03-services/plans-svc.md) |
| ingest-svc | [03-services/ingest-svc.md](03-services/ingest-svc.md) |
| aggregator | [03-services/aggregator.md](03-services/aggregator.md) |
| limits-svc | [03-services/limits-svc.md](03-services/limits-svc.md) |
| billing-svc | [03-services/billing-svc.md](03-services/billing-svc.md) |
| invoice-worker, payments-worker, notifier | [03-services/workers.md](03-services/workers.md) |
| analytics-svc | [03-services/analytics-svc.md](03-services/analytics-svc.md) |
| assets-svc | [03-services/assets-svc.md](03-services/assets-svc.md) |

## Architecture Decision Records

| # | Decision |
|---|---|
| [0001](adr/0001-go-workspace-monorepo.md) | Go workspace monorepo |
| [0002](adr/0002-kafka-for-event-log.md) | Kafka for the event log |
| [0003](adr/0003-rabbitmq-for-commands.md) | RabbitMQ for commands and jobs |
| [0004](adr/0004-shared-schema-rls-tenancy.md) | Shared schema + Row Level Security for multi-tenancy |
| [0005](adr/0005-orchestration-saga-for-billing.md) | Orchestration-based saga for invoicing |
| [0006](adr/0006-eventual-consistency-for-limits.md) | Eventually consistent limit enforcement |
| [0007](adr/0007-protobuf-event-schemas.md) | Protobuf event schemas with `buf breaking` |
| [0008](adr/0008-clickhouse-for-analytics.md) | ClickHouse for analytics |
| [0009](adr/0009-transactional-outbox-and-inbox.md) | Transactional outbox + inbox (offsets in the DB transaction) |
| [0010](adr/0010-money-and-quantity-representation.md) | Money and quantity representation |
| [0011](adr/0011-sandbox-environment.md) | Sandbox environment with a mock payment flow |
| [0012](adr/0012-per-tenant-aggregate-generations.md) | Per-tenant aggregate generations |
| [0013](adr/0013-s3-compatible-object-storage.md) | S3-compatible object storage (PDFs, multipart multi-file asset uploads) |
| [0014](adr/0014-idempotency-key-retention.md) | Idempotency keys live in month partitions that are dropped, not deleted |
| [0015](adr/0015-operator-console-stack.md) | The operator console is a Vue single-page app |
| [0016](adr/0016-provider-capabilities-and-routing.md) | Payment providers declare capabilities; routing is a stored rule set |
| [0017](adr/0017-advance-fixed-fees-and-change-modes.md) | Fixed fees are billed in advance; plan changes are explicit, previewed and credited |
| [0018](adr/0018-tenant-isolation-modes.md) | Two isolation modes: pooled (RLS) and cells (schema per tenant); built in stages, the job conversion is not finished ([runbook](runbooks/cells.md)) |

## Records

| Record | What it is |
|---|---|
| [plans/](plans/README.md) | The 22 work plans, in order, each with its gaps, research and acceptance |
| [roadmap-owner-list.md](roadmap-owner-list.md) | What was shipped from the owner's two lists of 2026-10-28; superseded by the plans, kept as history |
| [ui-test-report.md](ui-test-report.md) | A real-browser pass over the product (2026-10-21) and the real-stack specs it left |
| [code-quality/README.md](code-quality/README.md) | The code-quality report of 2026-10-21 (in Turkish) and its per-area files; the work it led to is [phase 49](phases/phase-49-code-quality-remediation.md) |
| [phases/phase-43-ci-repair.md](phases/phase-43-ci-repair.md) | Repairing main's CI and the nightly runs |

## Delivery plan

[phases/ROADMAP.md](phases/ROADMAP.md) lists what is left: the open phases, the runs that need real accounts, and known gaps.

## Conventions

- Diagrams are Mermaid, so GitHub renders them inline.
- Numbers marked **target** are goals. They are not measurements. Measured numbers are published in Phase 9.
- Code blocks are illustrative sketches (proto, SQL, Go interfaces). They are not implementation.
