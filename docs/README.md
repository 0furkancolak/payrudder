# Quotify documentation

Status: **implemented through Phase 18** (see the [roadmap](phases/ROADMAP.md)). These documents are the source of truth for the design. Code that disagrees
with them is a bug in the code or in the docs. Fix one of them in the same PR.

## Core

| Doc | What it answers |
|---|---|
| [00-vision.md](00-vision.md) | Why Quotify exists, who uses it, goals, non-goals, success criteria |
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
| [12-kubernetes.md](12-kubernetes.md) | Running Quotify on kind with Helm: charts, profiles, hooks, network policies, what is not covered |
| [13-admin-ui.md](13-admin-ui.md) | The operator console: views, the typed API client, tests |
| [15-billing-lifecycle.md](15-billing-lifecycle.md) | Yearly plans, fees in advance, upgrades and downgrades, the Paddle catalogue and API, credit notes, coupons and tax |
| [14-billing-platform.md](14-billing-platform.md) | Subscriptions, iyzico and Paddle, routing by country and currency, provider migration, reports: the design and what was built |

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

## Delivery plan

[phases/ROADMAP.md](phases/ROADMAP.md) has the phase table. Each phase has its own file with tasks
and acceptance criteria.

## Conventions

- Diagrams are Mermaid, so GitHub renders them inline.
- Numbers marked **target** are goals. They are not measurements. Measured numbers are published in Phase 9.
- Code blocks are illustrative sketches (proto, SQL, Go interfaces). They are not implementation.
