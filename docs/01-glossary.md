# 01 — Glossary

Terms are used with exactly these meanings across all docs and code. Code identifiers are in `monospace`.

| Term | Definition |
|---|---|
| **Tenant** (`tenant_id`) | A SaaS company using Quotify. The unit of isolation, rate limiting and Kafka partitioning. UUIDv7. |
| **API key** | Secret credential (`qk_live_…` / `qk_test_…`) that authenticates a tenant at the gateway. Stored only as a SHA-256 hash. |
| **Customer** (`customer_id`, `external_id`) | A tenant's end customer, the entity that gets invoiced. Tenants refer to customers by their own `external_id`. Quotify assigns an internal UUIDv7 `customer_id`. |
| **Meter** (`meter_code`) | Definition of what is measured and how it aggregates: `code` (e.g. `ai_tokens`), `aggregation` (`SUM`/`COUNT`/`MAX`), `value_property` (which event field holds the quantity), optional `filter`. **Immutable** once created. Changes create a new meter version. |
| **Usage event** (`UsageEvent`) | An immutable fact sent by a tenant: "customer C, meter M, quantity Q, at time T". |
| **Idempotency key** | Client-chosen string, unique per tenant, that identifies an event across retries. The same key is accepted once. |
| **Event ID** (`event_id`) | ULID that ingest-svc assigns when it first accepts an idempotency key. Downstream dedup uses `(tenant_id, event_id)`. |
| **Event time** (`event_time`) | When the usage happened, according to the tenant. Decides which **period** the event belongs to. |
| **Ingested-at** (`ingested_at`) | When ingest-svc durably accepted the event (server clock). Decides whether the event is **late**. |
| **Plan** | A named pricing package (e.g. "Pro"). Has immutable **plan versions**. |
| **Plan version** | Immutable snapshot of a plan's prices and entitlements. Subscriptions point to a version. Re-rating creates a new version or an override. |
| **Price** | How a meter's quantity becomes money within a plan version. Models: `PER_UNIT`, `TIERED_GRADUATED`, `PACKAGE`, `FLAT`. |
| **Entitlement / limit** | Maximum quantity of a meter per period in a plan version, with alert thresholds (e.g. 80%, 100%). |
| **Subscription** | Links a customer to a plan version from `starts_at` until `ends_at` (nullable). |
| **Billing period** (`period_id`) | Calendar month in UTC, `[start, end)`. Encoded as `YYYY-MM` (e.g. `2026-11`). |
| **Grace period** | Time after `period_end` during which events for that period still count as on time. Default 24 h, configurable per tenant, snapshotted per period. |
| **Cutoff** | `period_end + grace`. An event for period P is **on-time** iff `ingested_at < cutoff(P)`. Otherwise it is **late**. |
| **Late event** | Event whose `ingested_at ≥ cutoff` of its event-time period. Billed as an **adjustment line** on the next open invoice. |
| **Usage aggregate** | Authoritative per-`(tenant, customer, meter, period)` quantity, maintained by aggregator. |
| **Watermark** | Proof that aggregator has applied every on-time event of a period. Built from the outbox drain check plus Kafka end offsets ([07-correctness.md §4](07-correctness.md#4-late-arriving-events)). |
| **Period close** | Process that freezes usage for a period and starts invoicing once the watermark passes the cutoff. |
| **Rating** | Applying a plan version's prices to usage aggregates to get invoice line amounts. Pure function. |
| **Re-rating** | Rating a closed period again with different prices. Produces a correcting invoice delta. |
| **Re-aggregation / replay** | Rebuilding usage aggregates from the Kafka log (e.g. after fixing a bug). Writes into a new **aggregate generation**. |
| **Aggregate generation** | Version number of the aggregate set. Replay builds generation N+1 next to N, then switches atomically. |
| **Invoice** | Document billing a customer for a period. Lifecycle is driven by the **invoice saga**. |
| **Invoice line** | One meter × price result (or flat fee or adjustment) on an invoice. Amount in integer minor units. |
| **Billing run** | One execution of period close for a tenant and period. Contains one saga per customer invoice. |
| **Saga** | Long-running, orchestrated workflow with persisted state and compensating actions ([ADR 0005](adr/0005-orchestration-saga-for-billing.md)). |
| **Command** | Request for a worker to *do* something (render PDF, charge payment). Travels on RabbitMQ. May fail and be retried. |
| **Fact / event** | Something that *happened*. Travels on Kafka. Never retried, only re-read. |
| **Outbox** | Table written in the same transaction as business data. A relay publishes its rows to Kafka or RabbitMQ. |
| **Inbox** | Consumer-side table (dedup IDs and/or Kafka offsets) written in the same transaction as the effect. |
| **DLQ / DLX** | Dead-letter queue (Kafka topic) / dead-letter exchange (RabbitMQ) for messages that can't be processed. |
| **Poison message** | A message that fails deterministically however often it is retried. |
| **Minor units** | Integer subdivision of the currency (kuruş, cents). All stored and transferred money amounts. |
| **mock-psp** | Fake payment service provider with configurable latency, failure rate and idempotency semantics. |
