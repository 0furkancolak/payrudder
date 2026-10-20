# 10 — Local development

## Two commands

| Command | When | What it does |
|---|---|---|
| `make setup` | once per machine (and after `make clean`) | builds the pinned tools, starts the **infrastructure containers only** (Postgres, Redpanda, RabbitMQ, Redis, ClickHouse, MinIO, Mailpit), creates the topics and buckets, installs the console's packages |
| `make dev` | every day | runs the Go services **on your machine** (`quotifyctl dev`) and the console (Vite, port 3001), all with reload; Ctrl-C stops everything. `make dev ONLY=gateway,plans-svc` or `SKIP=analytics-svc` runs some |

The services' settings are the compose file's, read as `docker compose config`, with each container address turned into the host port the container publishes (`postgres:5432` becomes `localhost:5432`, `dns:///plans-svc:7001` becomes `localhost:7001`, Redpanda's `9092` becomes its external `19092`): one list of settings, not one for Docker and one for the host.
A service is rebuilt and restarted when a `.go`, `.sql` or `.yaml` file of its module or of `pkg/` changes; its log lines carry its name in colour. Tracing is off unless the observability profile is up. If another project holds port 5432, start with `QUOTIFY_PG_HOST_PORT=5440 make setup dev`.
Application images exist for CI and releases; `make dev-docker` builds and runs them when you want to check an image. `make mem-report` shows what every running container holds.

The sections below describe the compose stack in full.


Prerequisites: Docker (≥ 8 GB RAM for the full stack), Go 1.27+, `make`, `buf`, `sqlc`, `golangci-lint`, `k6` (optional),
`kind` + `helm` (Phase 10). `make tools` builds the Go-based tools pinned in `tools/go.mod` into `./bin`
(`go build tool`). `tools/` is **not** in `go.work`: golangci-lint, buf and sqlc pull hundreds of dependencies, and in a
workspace they would raise the versions selected for every service.

Docker memory: the infrastructure profile measured ~0.8 GiB resident at idle on Docker Desktop (arm64). Every container has
a memory limit, and the limits add up to ~3.6 GiB.

## Compose stack

File: `deploy/compose/compose.yaml`, with profiles so you can start only what you need.

### Infrastructure (`profile: infra`, the default)

| Service | Image (pinned in compose) | Ports (host) | Notes |
|---|---|---|---|
| postgres | `postgres:17.11-alpine` | 5432 (`QUOTIFY_PG_HOST_PORT`) | one instance. Init script creates `plans_db`, `ingest_db`, `aggregator_db`, `billing_db`, `payments_db`, `notifier_db`, `assets_db`, `mockpsp_db` and the per-service roles |
| redpanda | `redpandadata/redpanda` | 19092 (Kafka), 18081 (schema registry, unused), 9644 (admin) | single node, `--mode dev-container` |
| redpanda-console | `redpandadata/console` | 8080 (`QUOTIFY_CONSOLE_HOST_PORT`) | topic browser |
| rabbitmq | `rabbitmq:4-management` | 5672, 15672 (UI) | `definitions.json` loaded at boot |
| redis | `redis:7` | 6379 | `appendonly yes`, `appendfsync everysec` |
| clickhouse | `clickhouse/clickhouse-server` | 8123 (HTTP), 9000 (native) | |
| minio | `cgr.dev/chainguard/minio@sha256:…` | 9001 (S3 API), 9002 (console) | S3-compatible store ([ADR 0013](adr/0013-s3-compatible-object-storage.md)). Bucket `invoices` + sandbox lifecycle rule created by an init container (`mc`) |
| mailpit | `axllent/mailpit` | 1025 (SMTP), 8025 (UI) | |
| toxiproxy | `ghcr.io/shopify/toxiproxy` | 8474 (API) | `profile: chaos` only |

### Observability (`profile: obs`)

| Service | Ports |
|---|---|
| otel-collector | 4317 (OTLP gRPC), 4318 (OTLP HTTP) |
| prometheus | 9090 |
| tempo | 3200 |
| grafana | 3000 (anonymous admin locally, dashboards provisioned) |

### Quotify services (`profile: app`)

| Service | gRPC | HTTP | Metrics/health |
|---|---|---|---|
| gateway | — | **8000** (public API) | 9100 |
| plans-svc | 7001 | — | 9101 |
| ingest-svc | 7002 | — | 9102 |
| aggregator | 7003 | — | 9103 |
| limits-svc | 7004 | — | 9104 |
| billing-svc | 7005 | — | 9105 |
| analytics-svc | 7006 | — | 9106 |
| invoice-worker | — | — | 9107 |
| payments-worker | — | — | 9108 |
| notifier | — | — | 9109 |
| assets-svc | 7007 | — | 9111 |
| mock-psp | — | 8090 | 9110 | (sandbox payment provider, also used for live tenants in the local demo)

Every service exposes `/healthz` (liveness), `/readyz` (readiness: DB, broker reachable, partitions assigned) and `/metrics`
on its metrics port.

## Configuration

Environment variables only (12-factor), parsed by `pkg/config` into typed structs (`caarlos0/env`). Example:

```
QUOTIFY_ENV=local
QUOTIFY_SERVICE_NAME=ingest-svc
QUOTIFY_GRPC_ADDR=:7002
QUOTIFY_METRICS_ADDR=:9102
QUOTIFY_PG_APP_DSN=postgres://ingest_app:…@postgres:5432/ingest_db
QUOTIFY_PG_SYSTEM_DSN=postgres://ingest_system:…@postgres:5432/ingest_db
QUOTIFY_KAFKA_BROKERS=redpanda:9092
QUOTIFY_OTLP_ENDPOINT=otel-collector:4317
QUOTIFY_ALLOWED_ENVIRONMENTS=sandbox,live      # plans-svc, gateway; 'sandbox' only in values-sandbox.yaml

# invoice-worker, billing-svc (presign): any S3-compatible endpoint
S3_ENDPOINT=http://minio:9000
S3_REGION=us-east-1
S3_BUCKET=invoices
S3_FORCE_PATH_STYLE=true

# payments-worker: sandbox always uses mock-psp (not configurable)
MOCKPSP_URL=http://mock-psp:8090
PAYMENTS_LIVE_PROVIDER=mockpsp                 # local demo only; unset → live charges fail with PROVIDER_NOT_CONFIGURED
```

`.env.example` is committed. `.env` is git-ignored.

## Make targets (planned)

| Target | Does |
|---|---|
| `make tools` | Install pinned dev tools |
| `make gen` | `buf generate` + `sqlc generate` for all services |
| `make lint` | `golangci-lint run ./...` across the workspace + `buf lint` |
| `make test` | Unit tests for all modules |
| `make test-integration` | Integration tests (`-tags integration`, needs Docker) |
| `make up` / `make down` | Compose `infra` + `obs` + `app` profiles up/down |
| `make up-infra` | Only infrastructure (run services from your IDE) |
| `make migrate` | Run goose migrations for every service DB |
| `make topics` | Create Kafka topics with the configs from [04-messaging.md](04-messaging.md) (`rpk topic create …`, idempotent). `make up-infra` and `make up` run it. Compose Redpanda has topic auto-creation off, so a missing topic fails loudly |
| `make seed` | Create a demo account (currency/time zone/grace passed explicitly: `CURRENCY=TRY TZ=Europe/Istanbul GRACE=24h`), sandbox + live keys, meters (one fractional), plan, 10 customers covering every `pm_sandbox_*` scenario |
| `make demo` | Seed → send events → sandbox `ClosePeriodNow` → every payment scenario plays out → change the price → re-rate → CORRECTION invoices and receipts → links to Mailpit, Grafana, MinIO (`quotifyctl demo`; sandbox dunning on seconds; about 2.5 minutes) |
| `make e2e` | Run `tests/e2e` against the compose stack |
| `make web-check` / `make web-e2e` | The operator console ([13-admin-ui.md](13-admin-ui.md)): lint, types, build; Playwright smoke test against the running stack |
| `make chaos N=100000` | Run the correctness suite with N events: two replicas of ingest-svc, aggregator and limits-svc behind Toxiproxy, built with the `chaos` tag and armed, a chaos monkey that kills replicas, resets connections, stops Redpanda and wipes Redis, and assertions A1-A9 with a JSON report (`tests/chaos`, [09-testing-strategy.md](09-testing-strategy.md)) |
| `make bench-idempotency` | The partitioned-vs-DELETE measurement behind [ADR 0014](adr/0014-idempotency-key-retention.md) |
| `quotifyctl replay\|rerate\|dlq` | Operator CLI: rebuild a tenant into a new generation and promote it; price a closed period again; inspect and redrive dead letters (RabbitMQ and Kafka) |
| `make load S=ingest_steady` | Run a k6 scenario against the stack (`ingest_steady`, `ingest_spike`, `noisy_neighbor`, `entitlement_reads`, `month_close`); `K6_ENV="-e DURATION=2m -e EVENTS_PER_SEC=500"` overrides its parameters. Results stream to Prometheus (Grafana dashboard "Quotify / k6") and a JSON summary lands in `tests/load/results/` |
| `make kind-up` / `make kind-images` / `make helm-install` | A kind cluster, the service images loaded into it, and the two Helm releases ([12-kubernetes.md](12-kubernetes.md)). `KIND_PROFILE=kind-min KIND_CONFIG=deploy/kind/cluster-min.yaml` for a small Docker VM; `HELM_ARGS="-f deploy/helm/quotify/values-sandbox.yaml"` for the sandbox-only profile |
| `make e2e-kind` / `make netpol-test` | The e2e suite and the NetworkPolicy check against the kind cluster |
| `make helm-lint` | `helm lint` + `kubeconform` for every values file of both charts |
| `make clean` | Remove volumes in `.data/` |

## Typical loop

```bash
make up-infra && make migrate && make topics
go run ./services/ingest-svc/cmd/ingest-svc     # with .env loaded via direnv
```
