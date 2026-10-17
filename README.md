# Quotify

> Multi-tenant usage metering & billing platform in Go — Kafka, RabbitMQ, gRPC, Postgres RLS.

**Status: planning.** No implementation code yet. The design lives in [`docs/`](docs/README.md).

Quotify lets SaaS companies (tenants) send usage events — "customer X used 1,500 AI tokens",
"customer Y made 1 API call" — and turns them into correct, auditable invoices.
It's in the same family as Orb, Lago and Stripe's usage-based billing.

Correctness is the product:

- **No lost events.** Ingest is acknowledged only after a durable write (transactional outbox).
- **No double counting.** Idempotency keys at the edge, inbox/offset-in-transaction downstream.
- **Replayable history.** Kafka is the event log. A pricing change re-rates a closed period
  without touching raw events; a meter change re-aggregates from the log.
- **Near-real-time limits.** Redis atomic counters, reconciled against the authoritative aggregates.
- **A sandbox for every account.** Test keys hit an isolated sandbox tenant with a mock payment provider,
  deterministic payment scenarios and on-demand period close, locally and on Kubernetes.

## Why this repo exists

It's a portfolio project to show production-grade **Go microservices and distributed-systems** work.
Every technology choice has an ADR that explains *why* — see [`docs/adr/`](docs/adr/).

| Concern | Choice |
|---|---|
| Services | Go, gRPC (`buf`), Go workspace monorepo |
| Event log (facts) | Kafka API (Redpanda locally), `franz-go` |
| Task queue (commands) | RabbitMQ, `amqp091-go`, TTL + DLX retries |
| OLTP | PostgreSQL, `pgx` + `sqlc`, database-per-service, Row Level Security |
| Counters / rate limits | Redis (Lua) |
| Analytics | ClickHouse |
| Object storage | S3 API (MinIO locally and in kind) |
| Observability | OpenTelemetry, Prometheus, Grafana, Tempo, `slog` |
| Testing | `testcontainers-go`, k6, chaos/correctness suite |
| Deploy | Docker Compose, kind + Helm, GitHub Actions |

## Start here

- [Vision](docs/00-vision.md) — the problem, goals and non-goals
- [Architecture](docs/02-architecture.md) — system diagram and data flows
- [Correctness](docs/07-correctness.md) — how the ten hard distributed-systems problems are solved
- [Roadmap](docs/phases/ROADMAP.md) — 12 one-week phases

## License

TODO: add the Apache-2.0 `LICENSE` file when the repository goes public (decided, not yet added while the repo is private).
