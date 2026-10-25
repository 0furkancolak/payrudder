# Phase 08 — Observability end to end

## Goal

Prove a single trace spans HTTP → gRPC → outbox → Kafka → consumers and billing → RabbitMQ → workers → replies, and
ship dashboards, SLOs and alerts that make every correctness mechanism visible.

## Scope

**In:** audit and fix propagation on every hop, span links for batch consumers, messaging semantic conventions, all metrics
from [08-observability.md](../08-observability.md#metrics), per-tenant lag metric, six Grafana dashboards, Prometheus alert rules,
collector tail sampling, log/trace correlation.

**Out of scope:** Loki (optional), paging integrations, long-term metrics storage.

## Tasks

- [x] Propagation audit test: e2e test asserts one trace ID from gateway to aggregator span and from billing to worker reply (Tempo API query)
- [x] Batch consumer spans with links to producer contexts. 1% per-record child spans
- [x] Messaging span attributes per OTel semconv (`messaging.*`), `payrudder.tenant_id` on all spans
- [x] Saga transition spans and attributes
- [x] Implement missing metrics. `consumer_lag_seconds{tenant}` from `ingested_at`
- [x] Redpanda, RabbitMQ, Postgres, Redis, ClickHouse exporters/scrape configs
- [x] Dashboards: ingest, pipeline lag, correctness, billing, per-tenant drill-down, infra (JSON, provisioned)
- [x] Alert rules from [08-observability.md](../08-observability.md#alerts-prometheus-rules-deployprometheusrulesyaml) + `promtool test rules` unit tests
- [x] Collector tail sampling (errors, > 1 s)
- [ ] Grafana "explore trace from log" correlation (derived fields on `trace_id`)
- [ ] README screenshot of the end-to-end trace

## Deliverables

- Dashboards and alert rules in `deploy/`. A Tempo trace screenshot in README.

## Acceptance criteria

- [x] E2E test: `POST /v1/events` trace ID found on spans from gateway, ingest-svc, relay, aggregator, limits-svc and analytics-svc
- [x] E2E test: one invoice saga trace includes `invoice.render process` and `payment.charge process` spans and the reply processing span
- [x] `promtool test rules` passes for every alert, with a firing and a non-firing case each
- [x] Every metric in the docs table exists (test scrapes `/metrics` and checks names)
- [x] No metric has a `customer` or `event_id` label (lint test over scraped output)
- [ ] Killing aggregator for 60 s fires `ConsumerLagHigh` in Prometheus (manual check, documented)

## Dependencies

Phases 00–07 (all instrumented components exist).

## Risks / open questions

- Span links rendering in Grafana/Tempo can be awkward for batch consumers. Possibly add a per-batch "representative parent" for a nicer waterfall.
- Should `tenant` stay a metric label if tenant count grows? Fine for this project. Document the threshold (~1k).

## Notes (as built)

- **Spans.** `pkg/kafkax/trace.go` (`StartBatch`, `StartProduce`, debug marking), `pkg/rabbitx` (consumer span per delivery, trace
  injection on publish), `pkg/outbox` (publish spans; headers carry `traceparent`), `pkg/tenant` (sets `payrudder.tenant_id`
  on the server span). The gateway forwards `X-Payrudder-Debug: 1` as gRPC metadata for sandbox keys and the operator token;
  ingest copies it into the outbox headers.
- **Lag.** `consumer_lag_records{group,partition}` comes from the consumer's own high-water marks;
  `consumer_lag_seconds{group,partition,tenant}` is `now - ingested_at` of the last applied record, taken through
  `ConsumerConfig.RecordTime` (`kafkax.UsageRecordTime` for the usage topic).
- **Renames to match the table in this file:** `gateway_ratelimited{tenant,class}`, `saga_transitions`, `limits_thresholds_fired{pct}`,
  `ingest_events{tenant,result,error_code}`, `aggregator_events_applied{tenant,classification}`. New: `billing_runs{status}`,
  `saga_state{state}` (gauges read on scrape from billing's system pool), `billing_run_wait_watermark_seconds`, `payments{result}`,
  `ingest_outbox_depth`, `ingest_outbox_oldest_age_seconds`.
- **`dlq_messages{broker,queue_or_topic}`** is a Prometheus recording rule over RabbitMQ's per-queue metrics
  (`rabbitmq_detailed_queue_messages_ready` for `*.dlq`, from the `/metrics/detailed` scrape job) and Redpanda's `max_offset` for `*.dlq.*` topics (records ever dead-lettered: Redpanda exposes no start offset), not
  application code: the brokers already know their depth. The Kafka count is `max_offset` minus the `payrudderctl-redrive` group's committed
  offset, so redriving with `payrudderctl dlq redrive --broker kafka` (Phase 09) clears the alert.
- **Alerts** live in `deploy/prometheus/rules/payrudder.yaml` and are tested with `make test-rules` (`promtool`, in CI) with a firing and a
  quiet case each. `ServiceDown` was added to the list. `BillingRunStuck` fires on the `billing_runs{status="WAITING_WATERMARK"}`
  gauge staying above zero for an hour.
- **Dashboards** (`deploy/grafana/dashboards/*.json`) are provisioned; the tenant dashboard takes a `tenant` variable.
- **Exporters** run in the `obs` profile: `postgres-exporter`, `redis-exporter`; ClickHouse serves `:9363` natively, RabbitMQ `:15692`
  and Redpanda `:9644/public_metrics`.
- **Manual check for `ConsumerLagHigh`:** `docker stop payrudder-aggregator-1`, send events, and after about 6 minutes the alert is
  firing at <http://localhost:42990/alerts> (`for: 5m`). `docker start` clears it.
- Deferred: Loki and the "trace from log" derived field (optional in the scope), the README trace screenshot.
