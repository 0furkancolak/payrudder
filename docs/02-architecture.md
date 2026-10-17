# 02 — Architecture

## Principles

1. **Facts on Kafka, commands on RabbitMQ.** Kafka = event log (facts, replayable).
   RabbitMQ = task queue (commands, retries). See [ADR 0002](adr/0002-kafka-for-event-log.md) and [ADR 0003](adr/0003-rabbitmq-for-commands.md).
2. **Database per service.** No service reads another service's tables. Cross-service reads go
   through gRPC or a published topic.
3. **Every side effect is idempotent.** Each write has a natural dedup key, and the docs name it.
4. **No dual writes.** A DB write plus a message publish always goes through an outbox ([ADR 0009](adr/0009-transactional-outbox-and-inbox.md)).
5. **Aggregation and rating are separate.** Quantities are facts. Prices are applied later by a pure function.
   This makes re-rating cheap and safe.
6. **Determinism over wall clocks.** Period assignment and lateness come from event fields
   (`event_time`, `ingested_at`), never from "when the consumer happened to see it".

## System diagram

```mermaid
flowchart LR
    client[Tenant backend]

    subgraph edge[Edge]
        gw[gateway<br/>HTTP → gRPC<br/>auth, rate limit]
    end

    subgraph core[Core services]
        plans[plans-svc]
        ingest[ingest-svc]
        agg[aggregator]
        limits[limits-svc]
        billing[billing-svc<br/>saga orchestrator]
        analytics[analytics-svc]
    end

    subgraph workers[Workers]
        inv[invoice-worker]
        pay[payments-worker]
        notif[notifier]
    end

    subgraph kafka[Kafka / Redpanda]
        tEvents[(usage.events.v1)]
        tLimit[(usage.limit-exceeded.v1)]
        tEnt[(plans.entitlements.v1<br/>compacted)]
        tInv[(billing.invoices.v1)]
        tDlq[(*.dlq.v1)]
    end

    subgraph rmq[RabbitMQ]
        xCmd{{billing.commands}}
        xRep{{billing.replies}}
    end

    subgraph stores[State]
        pgPlans[(plans_db)]
        pgIngest[(ingest_db)]
        pgAgg[(aggregator_db)]
        pgBill[(billing_db)]
        redis[(Redis)]
        ch[(ClickHouse)]
        minio[(S3 / MinIO<br/>PDFs)]
    end

    psp[mock-psp]
    mail[Mailpit]

    client -->|HTTPS + API key| gw
    gw -->|gRPC| plans
    gw -->|gRPC| ingest
    gw -->|gRPC| limits
    gw -->|gRPC| billing
    gw -->|gRPC| analytics
    gw --- redis

    plans --- pgPlans
    plans -->|outbox| tEnt
    ingest --- pgIngest
    ingest -->|outbox relay| tEvents

    tEvents --> agg
    tEvents --> limits
    tEvents --> analytics
    tEnt --> limits

    agg --- pgAgg
    limits --- redis
    limits --> tLimit
    limits -.->|reconcile gRPC| agg
    tLimit --> analytics

    billing --- pgBill
    billing -->|gRPC| agg
    billing -->|gRPC| plans
    billing -->|gRPC watermark| ingest
    billing -->|outbox| xCmd
    billing -->|outbox| tInv
    xCmd --> inv
    xCmd --> pay
    xCmd --> notif
    inv --> xRep
    pay --> xRep
    notif --> xRep
    xRep --> billing
    tInv --> analytics

    inv --- minio
    pay -->|HTTP| psp
    notif -->|SMTP| mail
    analytics --- ch
```

## Service map

| Service | Kind | Owns | In | Out |
|---|---|---|---|---|
| [gateway](03-services/gateway.md) | Stateless edge | — (Redis keys for rate limits and API-key cache) | HTTP/JSON | gRPC |
| [plans-svc](03-services/plans-svc.md) | gRPC service | `plans_db` | gRPC | `plans.entitlements.v1` (outbox) |
| [ingest-svc](03-services/ingest-svc.md) | gRPC service + relay | `ingest_db` | gRPC | `usage.events.v1` (outbox) |
| [aggregator](03-services/aggregator.md) | Kafka consumer + gRPC | `aggregator_db` | `usage.events.v1` | gRPC reads, `usage.events.dlq.v1` |
| [limits-svc](03-services/limits-svc.md) | Kafka consumer + gRPC | Redis `lim:*` keys | `usage.events.v1`, `plans.entitlements.v1` | `usage.limit-exceeded.v1` |
| [billing-svc](03-services/billing-svc.md) | Saga orchestrator + gRPC | `billing_db` | gRPC, `billing.replies` | `billing.commands`, `billing.invoices.v1` |
| [invoice-worker](03-services/workers.md#invoice-worker) | RabbitMQ worker | S3 bucket `invoices` (MinIO locally, [ADR 0013](adr/0013-s3-compatible-object-storage.md)) | `invoice.render` | `billing.replies` |
| [payments-worker](03-services/workers.md#payments-worker) | RabbitMQ worker | `payments_db` | `payment.charge` | `billing.replies` |
| [notifier](03-services/workers.md#notifier) | RabbitMQ worker | `notifier_db` | `notification.send` | `billing.replies` |
| [analytics-svc](03-services/analytics-svc.md) | Kafka consumer + gRPC | ClickHouse `quotify` DB | `usage.events.v1`, `usage.limit-exceeded.v1`, `billing.invoices.v1` | gRPC reads |
| [assets-svc](03-services/assets-svc.md) | gRPC service | `assets_db` + S3 prefix `{tenant}/assets/` | gRPC (incl. client streaming) | presigned S3 URLs |
| mock-psp | Fake external HTTP API; the sandbox payment provider ([ADR 0011](adr/0011-sandbox-environment.md)) | own Postgres schema | HTTP | — |

## Flow 1 — Ingest

```mermaid
sequenceDiagram
    autonumber
    participant C as Tenant backend
    participant G as gateway
    participant I as ingest-svc
    participant DB as ingest_db
    participant R as outbox relay
    participant K as usage.events.v1
    C->>G: POST /v1/events (batch ≤ 500, Idempotency-Key per event)
    G->>G: verify API key, resolve tenant, rate-limit (Redis GCRA)
    G->>I: IngestEvents(tenant_id in metadata)
    I->>I: validate (meter exists, customer exists, event_time window)
    I->>DB: BEGIN, SET LOCAL app.tenant_id
    I->>DB: INSERT idempotency_keys ON CONFLICT DO NOTHING
    I->>DB: INSERT outbox (only for new keys)
    I->>DB: COMMIT
    I-->>G: per-event ACCEPTED / DUPLICATE / REJECTED
    G-->>C: 202 + per-event results
    loop every 50 ms or on NOTIFY
        R->>DB: SELECT … FOR UPDATE SKIP LOCKED LIMIT 1000
        R->>K: produce (key = tenant_id, idempotent producer)
        R->>DB: DELETE published rows
    end
```

The client gets `202` only after the commit, so an acknowledged event is never lost.
Publishing is at-least-once: the relay can crash after producing and before deleting.
Downstream dedups on `event_id`.

## Flow 2 — Aggregation and limits

```mermaid
sequenceDiagram
    autonumber
    participant K as usage.events.v1
    participant A as aggregator
    participant ADB as aggregator_db
    participant L as limits-svc
    participant RD as Redis
    participant KL as usage.limit-exceeded.v1
    K->>A: poll batch (partition p, offsets n..m)
    A->>ADB: BEGIN
    A->>ADB: INSERT processed_events ON CONFLICT DO NOTHING RETURNING event_id
    A->>ADB: UPSERT usage_aggregates += Σ(new events) / late_usage
    A->>ADB: UPSERT consumer_offsets(p) = m+1
    A->>ADB: COMMIT
    Note over A: Kafka offset commit is advisory only.<br/>Restart seeks to consumer_offsets.
    K->>L: poll batch (own consumer group)
    L->>RD: EVALSHA apply_batch.lua (offset guard + INCRBY + threshold check)
    RD-->>L: crossed thresholds
    L->>KL: produce LimitExceeded (deterministic event_id)
```

## Flow 3 — Period close and invoice saga

```mermaid
sequenceDiagram
    autonumber
    participant B as billing-svc
    participant I as ingest-svc
    participant A as aggregator
    participant P as plans-svc
    participant X as billing.commands
    participant W as workers
    B->>B: scheduler: now ≥ cutoff(P) for tenant T
    B->>I: GetOutboxWatermark → oldest unpublished ingested_at
    B->>A: GetIngestWatermark(period) → caught_up?
    B->>A: GetPeriodUsage(T, P, generation)
    B->>P: ListSubscriptions(T, P) + plan versions
    B->>B: rate → invoice DRAFT → FINALIZED (number assigned)
    B->>X: RenderInvoicePdf (via outbox)
    X->>W: invoice-worker
    W-->>B: PdfRendered (billing.replies)
    B->>X: ChargePayment
    X->>W: payments-worker → mock-psp
    W-->>B: PaymentSucceeded / PaymentFailed
    B->>X: SendNotification
    W-->>B: NotificationSent
    B->>B: saga COMPLETED, publish InvoicePaid to billing.invoices.v1
```

Full state machine: [03-services/billing-svc.md](03-services/billing-svc.md#saga-state-machine).

## Synchronous dependencies (gRPC)

| Caller | Callee | Method(s) | On callee outage |
|---|---|---|---|
| gateway | plans-svc | `ResolveApiKey` | Serve from Redis cache (60 s TTL). After expiry → `503` |
| gateway | ingest-svc | `IngestEvents` | `503` + `Retry-After`. Client retries with the same idempotency keys |
| ingest-svc | plans-svc | `GetMeter`, `ResolveCustomer` | In-process LRU + Redis cache. Cache miss on outage → `UNAVAILABLE` |
| limits-svc | aggregator | `GetUsageBatch` (reconcile) | Skip the reconciliation cycle. Alert if skipped > 3 times |
| billing-svc | ingest-svc, aggregator, plans-svc | watermark, usage, subscriptions | Billing run waits (state `WAITING_WATERMARK`) and retries with backoff |

The hot path (ingest) has exactly one synchronous hop after the gateway. Plans lookups on it are cached.

## Environments

| Level | Sandbox | Live |
|---|---|---|
| Tenant (every deployment) | `qk_test_` keys → sandbox tenant. Payments always via mock-psp with scenario tokens. Early period close. `SBX-` invoices | `qk_live_` keys → live tenant. Payments via `PAYMENTS_LIVE_PROVIDER` (v1: mock-psp in the local demo only) |
| Deployment | Compose default and kind `values-kind.yaml` run both. `values-sandbox.yaml` (namespace `quotify-sandbox`) runs sandbox-only, for a public demo | — (no production deployment in scope) |

## Deployment shape

- Every service is a single Go binary from `services/<name>/cmd/<name>`.
- Stateless replicas behind Kubernetes Services. Kafka consumers scale up to the partition count
  (`usage.events.v1` has 12 partitions → ≤ 12 aggregator pods).
- ingest-svc runs the HTTP-free gRPC server and the outbox relay in the same binary.
  The relay is a separate goroutine pool with `SKIP LOCKED`, so many replicas are safe.
- billing-svc's scheduler uses a Postgres advisory lock per `(tenant_id, period_id)` so only one pod
  drives a given billing run.
