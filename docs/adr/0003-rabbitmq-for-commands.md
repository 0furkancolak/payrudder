# ADR 0003 — RabbitMQ for commands and jobs

- Status: Accepted
- Date: 2026-10-17

## Context

Invoicing triggers **commands**: render this PDF, charge this payment, send this email. Each command has exactly one
logical handler, can fail transiently (PSP 5xx, SMTP timeout), needs **delayed retries with backoff**, and needs a
**dead-letter** path that a human inspects. Per-message acknowledgement and per-message redelivery matter more than replay.
Replaying "charge payment" from a log would be harmful.

> Kafka = event log (facts, replayable). RabbitMQ = task queue (commands, retries).

## Decision

- **RabbitMQ 4** with **quorum queues** for all command and reply queues.
- Client: **`github.com/rabbitmq/amqp091-go`**, wrapped in `pkg/rabbitx` (reconnect, channel pool, publisher confirms, topology).
- Topology: `billing.commands` (direct) → one queue per command type. Replies on `billing.replies`.
- Retries: per-tier **TTL retry queues** (5 s, 30 s, 2 min) dead-lettering back to the work exchange. Exhausted or poison messages
  go to `billing.commands.dlx` → `{queue}.dlq`. Quorum `x-delivery-limit=5` catches crash loops.
- Commands are published from billing-svc's **outbox** with publisher confirms. Workers ack only after their side effect and their
  reply publish are confirmed.

## Alternatives considered

| Option | Why not |
|---|---|
| Kafka for commands too | No per-message ack/nack. Retries need retry topics and a delay mechanism, and one slow command blocks its partition. Possible (and common), but a worse fit for job semantics |
| Delayed-message exchange plugin | Plugin dependency, and it doesn't replicate (not quorum-safe). TTL queues are built in |
| Per-message TTL on one retry queue | Head-of-line blocking: a 2 min message blocks a 5 s one behind it. Per-queue TTL tiers avoid this |
| Postgres job queue (River, `SKIP LOCKED`) | Very viable, and arguably simpler. Rejected to show broker-based work queues and because workers would need billing DB access |
| Classic mirrored queues | Deprecated. Quorum queues are the replicated default and support delivery limits |

## Consequences

- Clear semantics: commands are routed, acked, retried and dead-lettered individually.
- Two brokers to operate. That's acceptable and intentional, and justified by the facts/commands split.
- Long delays (dunning: days) are **not** done with RabbitMQ TTLs. They're scheduled in billing-svc's DB. RabbitMQ handles seconds to minutes.
- Exactly-once isn't provided by RabbitMQ, so workers dedup by `command_id` and the saga guards transitions by version.
