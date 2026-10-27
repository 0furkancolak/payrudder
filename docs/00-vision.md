# 00 — Vision

## Problem

Usage-based pricing ("$0.002 per 1K tokens", "first 10K API calls free") is now the default for
infrastructure and AI products. Building the metering behind it is deceptively hard:

- Clients retry, so events arrive **more than once**.
- Networks and batch uploads deliver events **late and out of order**.
- Consumers crash **between a database write and an offset commit**.
- Prices change, and finance asks to **recompute last month**.
- A single large customer can **starve** every other tenant.

A billing system can't be "mostly right". One double-counted event is a support ticket.
One lost event is lost revenue. Payrudder's purpose is to make these failure modes explicit
and handle each one with a named, tested mechanism.

## Users

| Persona | Who | Needs |
|---|---|---|
| **Tenant developer** | Engineer at a SaaS company integrating Payrudder | Simple ingest API, idempotency guarantees, clear errors, entitlement checks |
| **Tenant finance/ops** | Billing owner at the SaaS company | Correct invoices, explainable line items, re-rating after a price change |
| **End customer** | The tenant's customer (never talks to Payrudder directly) | An invoice that matches what they used |
| **Payrudder operator** | Whoever runs the platform (here: the author) | Observability, per-tenant isolation, replay tooling, safe deploys |

## Goals

1. **Correct ingest.** Every accepted event is counted exactly once in billing aggregates,
   even across client retries, producer retries, consumer crashes and pod restarts.
2. **High-volume ingest.** Horizontal scaling of every stateless service. Kafka partitions bound
   per-tenant parallelism.
3. **Near-real-time limits.** Entitlement checks reflect usage within seconds (eventually consistent,
   bounded staleness, documented).
4. **Deterministic period close.** Whether an event is late is a pure function of its data,
   so replays reproduce the same invoices.
5. **Reliable invoicing workflow.** Orchestrated saga with explicit states, retries, compensations,
   and a dead-letter path that a human can inspect.
6. **Re-rating and replay.** Recompute a closed period's charges after a pricing change, and rebuild
   aggregates from the log after a meter definition change, without double billing.
7. **Tenant isolation.** RLS-enforced data isolation, per-tenant rate limits and quotas, and per-tenant
   lag visibility.
8. **Operability.** One trace from HTTP request → Kafka → consumer → RabbitMQ → worker.
   Dashboards and SLOs from day one.

## Non-goals

These are out of scope on purpose, to keep the project to ~12 weeks for one developer:

- Real payment providers (Stripe, Adyen…). Payments are **mocked** by `mock-psp`, behind a `PaymentProvider` interface.
  Every account gets a **sandbox** with deterministic payment scenarios ([ADR 0011](adr/0011-sandbox-environment.md)).
- Taxes, VAT, tax engines.
- Multi-currency and FX. Each tenant **chooses one currency** at creation (immutable). There's no conversion, and an invoice
  never mixes currencies.
- Proration, mid-period plan changes, anchored (non-calendar) billing periods.
  Periods are **calendar months in the time zone the tenant chooses** at creation (immutable). Grace is also chosen at creation (0–7 days).
- Credit notes beyond the single "adjustment line" mechanism used for late events and re-rating.
- Prepaid credits / wallets, commitments, minimums.
- Per-event pricing functions beyond: per-unit, tiered (graduated), package, flat fee.
- Approximate aggregations (HyperLogLog and the like). Aggregations in scope, all exact: `SUM`, `COUNT`, `MAX`, `UNIQUE` (distinct values of a property) and `LAST` (the latest value) ([ADR 0025](adr/0025-unique-and-last-aggregations.md)).
- Customer-facing portal. An optional admin UI comes in Phase 12.
- Multi-region active-active, cross-region replication.
- Webhook delivery to tenants (limit-exceeded events are queryable and published to Kafka.
  Tenant webhooks are a documented extension point).
- Strong (linearizable) limit enforcement. Limits are eventually consistent by design ([ADR 0006](adr/0006-eventual-consistency-for-limits.md)).

## Success criteria

The project is "done" when all of these are demonstrably true:

| # | Criterion | How it's proven |
|---|---|---|
| S1 | 1M events with 5% duplicates, random pod kills every 10–30 s → aggregate totals **exactly** equal the generator's expected totals | `tests/chaos` suite, run in CI nightly and recorded in `docs/benchmarks.md` (Phase 9) |
| S2 | A price change on a closed period produces a re-rated invoice whose delta is exactly the expected amount, and no usage is counted twice | Integration test + demo script (Phase 11) |
| S3 | Late events never change a finalized invoice. They appear as adjustment lines on the next one | Integration test (Phase 5) |
| S4 | A single trace spans gateway → ingest → Kafka → aggregator and billing → RabbitMQ → worker | Screenshot in README from Tempo (Phase 8) |
| S5 | A noisy tenant at 10× its quota doesn't move other tenants' ingest p99 by more than 20% | k6 scenario (Phase 9) — **target** |
| S6 | `make up && make demo` runs the full flow on a laptop in < 5 minutes | Phase 11 |
| S7 | Every architectural choice has an ADR with alternatives considered | `docs/adr/` |

Throughput and latency **targets** (not measurements) are in [08-observability.md](08-observability.md#slos).
