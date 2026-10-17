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

- [ ] Propagation audit test: e2e test asserts one trace ID from gateway to aggregator span and from billing to worker reply (Tempo API query)
- [ ] Batch consumer spans with links to producer contexts. 1% per-record child spans
- [ ] Messaging span attributes per OTel semconv (`messaging.*`), `quotify.tenant_id` on all spans
- [ ] Saga transition spans and attributes
- [ ] Implement missing metrics. `consumer_lag_seconds{tenant}` from `ingested_at`
- [ ] Redpanda, RabbitMQ, Postgres, Redis, ClickHouse exporters/scrape configs
- [ ] Dashboards: ingest, pipeline lag, correctness, billing, per-tenant drill-down, infra (JSON, provisioned)
- [ ] Alert rules from [08-observability.md](../08-observability.md#alerts-prometheus-rules-deployprometheusrulesyaml) + `promtool test rules` unit tests
- [ ] Collector tail sampling (errors, > 1 s)
- [ ] Grafana "explore trace from log" correlation (derived fields on `trace_id`)
- [ ] README screenshot of the end-to-end trace

## Deliverables

- Dashboards and alert rules in `deploy/`. A Tempo trace screenshot in README.

## Acceptance criteria

- [ ] E2E test: `POST /v1/events` trace ID found on spans from gateway, ingest-svc, relay, aggregator, limits-svc and analytics-svc
- [ ] E2E test: one invoice saga trace includes `invoice.render process` and `payment.charge process` spans and the reply processing span
- [ ] `promtool test rules` passes for every alert, with a firing and a non-firing case each
- [ ] Every metric in the docs table exists (test scrapes `/metrics` and checks names)
- [ ] No metric has a `customer` or `event_id` label (lint test over scraped output)
- [ ] Killing aggregator for 60 s fires `ConsumerLagHigh` in Prometheus (manual check, documented)

## Dependencies

Phases 00–07 (all instrumented components exist).

## Risks / open questions

- Span links rendering in Grafana/Tempo can be awkward for batch consumers. Possibly add a per-batch "representative parent" for a nicer waterfall.
- Should `tenant` stay a metric label if tenant count grows? Fine for this project. Document the threshold (~1k).
