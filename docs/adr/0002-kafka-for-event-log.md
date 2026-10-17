# ADR 0002 — Kafka for the event log

- Status: Accepted
- Date: 2026-10-17

## Context

Usage events are **facts**: immutable, high-volume, and needed by several independent consumers (aggregator, limits,
analytics, future ones). Billing correctness requires that we can **replay** history months later (re-aggregation after a bug).
Consumers must scale horizontally while keeping a tenant's events together.

> Kafka = event log (facts, replayable). RabbitMQ = task queue (commands, retries).

## Decision

- Use the **Kafka protocol** for facts: `usage.events.v1`, `usage.limit-exceeded.v1`, `plans.entitlements.v1` (compacted),
  `billing.invoices.v1`, and the DLQ topics. Full catalogue: [04-messaging.md](../04-messaging.md).
- **Redpanda** for local dev and kind (single binary, Kafka-API compatible, fast start-up). The code targets the Kafka API only,
  so Apache Kafka works unchanged.
- Client: **`franz-go`** (`kgo`): pure Go, idempotent producer, cooperative rebalancing, `ListOffsetsAfterMilli` for replay,
  fine-grained partition assignment callbacks (needed to seek to DB-stored offsets).
- Partition key: **`tenant_id`**. `usage.events.v1` has 12 partitions. Retention: 400 days for replay.

## Alternatives considered

| Option | Why not |
|---|---|
| RabbitMQ streams | Has replay, but a smaller ecosystem and tooling. Mixing both roles in one broker blurs the facts/commands distinction this project wants to show |
| NATS JetStream | Good fit technically. Kafka is the industry standard for event logs, and the portfolio goal favours it |
| Postgres as the log (outbox only, consumers poll) | No independent consumer groups, no cheap replay at volume, couples consumers to ingest's DB |
| `confluent-kafka-go` | cgo + librdkafka. Harder cross-compilation and static images |
| `segmentio/kafka-go` | Simpler API, weaker consumer-group features (no cooperative-sticky, less control over assignment) |
| Kafka transactions / EOS end to end | Only covers Kafka→Kafka. Our sinks are Postgres/Redis/ClickHouse. See [ADR 0009](0009-transactional-outbox-and-inbox.md) |

## Consequences

- Consumers are independent: adding analytics or a new consumer doesn't touch producers.
- Replay is a consumer-group operation, not a data export.
- Long retention costs disk: 400 days × event volume. Locally retention is 30 days. In production, tiered storage (Redpanda or Kafka KIP-405)
  would hold old segments in object storage. That's documented, not built.
- Per-tenant partitioning creates hot-partition risk. See [07-correctness.md §7](../07-correctness.md#7-noisy-neighbor).
- At-least-once delivery means every consumer must be idempotent. That's the explicit design of every handler.
