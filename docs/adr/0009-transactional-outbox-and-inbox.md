# ADR 0009 — Transactional outbox + inbox (offsets in the DB transaction)

- Status: Accepted
- Date: 2026-10-17

## Context

Several services must change their database **and** publish a message (ingest → Kafka, plans → Kafka, billing →
RabbitMQ + Kafka). Several consumers must apply a message to a database exactly once despite at-least-once delivery.
Dual writes and "commit offset after DB write" both have well-known crash windows.

## Decision

**Producer side: transactional outbox.**
- Business rows and `outbox` rows are written in one transaction.
- A relay (`pkg/outbox`) selects rows with `FOR UPDATE SKIP LOCKED`, publishes (Kafka idempotent producer or AMQP with
  publisher confirms), then deletes them in the same transaction. Woken by `LISTEN/NOTIFY`, with a 50 ms polling fallback.
- Trace context is stored in `outbox.headers`.

**Consumer side: inbox.**
- **aggregator:** Kafka offsets are stored in `consumer_offsets` **in the same transaction** as the aggregate updates, and the consumer
  seeks to them on assignment. Plus `processed_events` dedup by `event_id` for duplicates that have *different* offsets
  (relay republish, redrive).
- **limits-svc:** the same idea in Redis: offset guard and counter updates in one Lua script.
- **workers / billing replies:** dedup tables keyed by `command_id` / `message_id`.

## Alternatives considered

| Option | Why not |
|---|---|
| Dual write (DB then publish) | Loses messages on crash between the two |
| Publish then DB | Phantom messages for rolled-back transactions |
| CDC (Debezium) on WAL | Robust and no polling, but adds Kafka Connect (JVM) to operate. Overkill for one developer |
| Kafka transactions (EOS) | Atomic only within Kafka. Our sinks are Postgres/Redis |
| Commit Kafka offset after DB commit | Crash between them → reprocessing → double count unless effects are idempotent. We make effects idempotent *and* atomic with progress |
| Only `processed_events`, Kafka-stored offsets | Works (dedup alone catches redelivery) but reprocesses whole batches after every crash. DB offsets make restarts exact and cheap |

## Consequences

- Exactly-once **effect** with at-least-once transport, provable by the chaos suite.
- Extra writes per event (outbox insert + delete. `processed_events` insert). Mitigated by batching and partition dropping.
- Publish latency is bounded by the relay (**target** p99 < 200 ms), not by the request.
- The relay is a generic `pkg/outbox` component reused by three services, so one implementation gets thoroughly tested.
