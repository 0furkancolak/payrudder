# 08 — Observability

Stack: **OpenTelemetry SDK** (traces + metrics) → **OTel Collector** → **Tempo** (traces) and
**Prometheus** (metrics, via the collector's Prometheus exporter or direct `/metrics` scrape) → **Grafana**.
Logs: `log/slog` JSON to stdout, with `trace_id`/`span_id` injected → **Vector** (Docker socket) → **Loki** → Grafana.

## Logs and the service map

Everything is in the compose `obs` profile (`make up` starts it) and opens in Grafana at
`localhost:3000`, which lands on **Quotify / Services**.

- **Vector** (`deploy/vector/vector.yaml`) tails every container of the compose project and labels each line with
  `service` (compose service) and `level` (slog level, lower-cased; `unknown` for infrastructure that does not log
  JSON). Only those two are labels, so the index stays tiny. `trace_id` stays inside the JSON line.
- **Loki** (`deploy/loki/loki.yaml`) is a single binary on local disk: 72 h retention, 16 MiB caches, no ruler.
- **Tempo's metrics generator** turns traces into `traces_service_graph_*` and `traces_spanmetrics_*` and remote-writes
  them to Prometheus. That is what draws the service map (calls, failures and message hops between services, and the
  databases and brokers behind them) and the per-service rate, error and latency panels.
- Everything links: a log line's `trace_id` opens the trace (Loki derived field), a span opens the logs of its service
  around that time, and the service map's nodes link to their metrics.

Dashboards: **Services** (service map, RED per service, edge latency, message hop latency, failed traces, memory of the
services and of the monitoring stack) and **Logs** (volume by level and service, filterable stream).

Footprint at idle: Prometheus ~70 MiB, Loki ~90 MiB, Tempo ~45 MiB, Vector ~12 MiB, collector ~60 MiB, Grafana ~140 MiB.
Each has a `GOMEMLIMIT` below its container limit so the GC works before the kernel does.


## Tracing

### Propagation

W3C Trace Context (`traceparent`, `tracestate`) everywhere, via `otel.GetTextMapPropagator()`.

| Hop | Carrier | Implementation |
|---|---|---|
| HTTP → gateway | HTTP headers | `otelhttp` |
| gRPC | gRPC metadata | `otelgrpc` stats handlers (client + server) |
| ingest → outbox → Kafka | `outbox.headers` jsonb → Kafka record headers | `pkg/otelx/kafka.go`: `TextMapCarrier` over `[]kgo.RecordHeader` |
| Kafka → consumer | record headers | consumer span per record batch with **span links** to each record's producer context (a batch has many parents). Per-record child spans are sampled 1% |
| billing → outbox → RabbitMQ | `outbox.headers` → AMQP `Table` headers | `pkg/otelx/amqp.go`: `TextMapCarrier` over `amqp.Table` |
| RabbitMQ → worker → reply | AMQP headers | consumer span is a child of the command's context. The reply carries the worker's context |

The outbox is the subtle part: the trace context of the *originating request* is stored with the row, so the relay's
produce span and the consumer span join the request trace even though they run seconds later in another process.

### Span naming & attributes

- Messaging spans follow OTel semantic conventions: `usage.events.v1 publish`, `usage.events.v1 process`,
  `messaging.system=kafka|rabbitmq`, `messaging.destination.name`, `messaging.kafka.partition`, `messaging.kafka.offset`.
- Every span carries `quotify.tenant_id`. Spans for events carry `quotify.event_id` (on per-record spans only).
- Saga spans: `saga.transition` with `quotify.saga_id`, `quotify.saga.from`, `quotify.saga.to`.

### Sampling

Parent-based. Root sampling: 100% locally, 10% in kind, **always** for errors and for requests with header
`x-quotify-debug: 1` (sandbox tenants and operators only; the gateway ignores it otherwise). Tail sampling in the
collector (`deploy/otel/collector.yaml`) keeps traces with errors, duration > 1 s or a debug mark, plus
`QUOTIFY_TRACE_SAMPLE_PERCENT` (default 100) of the rest.

Batch consumers (aggregator, limits, analytics) process many producers' records in one span, so the batch span
(`<topic> process`) carries **links** to up to 128 producer contexts. Records that are debug-marked, and 1 % of the
others, also get a span of their own whose parent is the producer's span, which is what keeps one trace id across
services (`pkg/kafkax/trace.go`). Billing stores `traceparent` in the outbox row it writes in the transition's
transaction, so the relay, the worker and the reply all continue the saga's trace; each transition is a
`saga.transition` span (`saga.id`, `saga.from`, `saga.to`, `saga.cause`).

## Metrics

RED for every gRPC/HTTP endpoint (from `otelgrpc`/`otelhttp`). Business and correctness metrics:

| Metric | Type | Labels | Why |
|---|---|---|---|
| `ingest_events_total` | counter | `tenant`, `result` (accepted/duplicate/rejected), `error_code` | Volume, dup rate |
| `ingest_outbox_depth` | gauge | — | Kafka health / back-pressure |
| `ingest_outbox_oldest_age_seconds` | gauge | — | Relay lag, watermark blocker |
| `relay_publish_duration_seconds` | histogram | `topic` | |
| `consumer_lag_records` | gauge | `group`, `partition` | From Kafka offsets (via collector `kafkametrics` or Redpanda metrics) |
| `consumer_lag_seconds` | gauge | `group`, `partition`, `tenant` | `now − ingested_at` of last applied record. **Per-tenant fairness** |
| `aggregator_events_applied_total` | counter | `tenant`, `classification` (on_time/late) | Late % per tenant |
| `aggregator_duplicates_skipped_total` | counter | `source` (processed_events) | Proves dedup is working |
| `aggregator_frozen_period_violations_total` | counter | `tenant` | Must stay 0 |
| `limits_reconcile_corrections_total` | counter | `reason` | Drift between Redis and aggregator |
| `limits_reconcile_skipped_total` | counter | `reason` | |
| `limits_thresholds_fired_total` | counter | `pct` | |
| `billing_runs` | gauge | `status` | Stuck runs |
| `billing_run_wait_watermark_seconds` | histogram | — | Close latency |
| `saga_transitions_total` | counter | `from`, `to` | Funnel |
| `saga_state` | gauge | `state` | Stuck sagas |
| `payments_total` | counter | `result` | |
| `dlq_messages` | gauge | `broker`, `queue_or_topic` | Must alert |
| `gateway_ratelimited_total` | counter | `tenant`, `class` | Noisy neighbors |

**Label cardinality:** `tenant` is allowed (bounded by the number of tenants, hundreds at most in this project).
`customer` and `event_id` are **never** metric labels. They go in traces and logs.

## Logging

- `slog.NewJSONHandler`, a handler wrapper adds `trace_id`, `span_id`, `tenant_id`, `service`, `version`.
- Levels: `INFO` for state transitions (saga, billing run), `WARN` for reconciled drift and retries, `ERROR` for DLQ and failures.
- No payloads, no API keys, no emails at INFO. A `slog.LogValuer` redacts sensitive types.

## Dashboards (Grafana, provisioned from `deploy/grafana/dashboards/*.json`)

1. **Ingest overview:** events/s by result, p50/p99 latency, outbox depth and age, 429s by tenant.
2. **Pipeline lag:** records and seconds lag per group/partition, per-tenant lag heatmap, DLQ depth.
3. **Correctness:** duplicates skipped, late %, reconcile corrections, frozen-period violations, ingest-accepted vs aggregator-applied
   (should converge to equal).
4. **Billing:** runs by status, watermark wait, saga funnel (Sankey of transitions), stuck sagas by state, payment outcomes.
5. **Per-tenant drill-down:** templated by `tenant`, so you can see every panel for one tenant.
6. **Infra:** Postgres (pg_exporter), Redpanda, RabbitMQ (built-in Prometheus plugin), Redis, ClickHouse.

## SLOs

All numbers are **targets** for a laptop-class local/kind setup. They'll be replaced with measurements in Phase 9.

| SLO | Target | SLI |
|---|---|---|
| Ingest availability | 99.9% of valid requests get `202` | non-5xx / total at gateway |
| Ingest latency | p99 < 50 ms at 5k events/s | gateway server duration, `POST /v1/events` |
| Freshness: ingest → aggregate | p99 < 5 s | `consumer_lag_seconds{group="aggregator"}` |
| Freshness: ingest → limit counter | p99 < 2 s | `consumer_lag_seconds{group="limits"}` |
| Period close | 99% of billing runs `COMPLETED` within cutoff + 30 min | billing_runs timestamps |
| Correctness | 0 frozen-period violations, 0 unexplained aggregate mismatches | correctness dashboard |
| DLQ | 0 messages older than 1 h | `dlq_messages` |

## Alerts (Prometheus rules, `deploy/prometheus/rules/*.yaml`)

- `OutboxStalled`: `ingest_outbox_oldest_age_seconds > 60` for 2m
- `ConsumerLagHigh`: `consumer_lag_seconds > 30` for 5m (per group)
- `TenantLagOutlier`: one tenant's lag > 5× the median for 10m
- `DLQNotEmpty`: `dlq_messages > 0` for 5m
- `BillingRunStuck`: run in `WAITING_WATERMARK` > 1h after cutoff
- `SagaStuck`: `saga_state{state=~".*_FAILED|PAYMENT_UNKNOWN"} > 0` for 15m
- `FrozenPeriodViolation`: any increase (page)
- `ReconcileDriftHigh`: corrections > 1% of active keys per cycle
