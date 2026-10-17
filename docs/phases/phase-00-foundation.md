# Phase 00 — Foundation

## Goal

A repository where any service can be added in minutes: Go workspace, protobuf toolchain, a local stack with every
infrastructure dependency, CI, and the logging/tracing skeleton that every later phase uses.

## Scope

**In:** `go.work`, module skeletons, `buf` setup, `pkg/{config,logx,otelx,grpcx,health,idgen,testkit}` minimal versions,
Docker Compose (infra + obs profiles), Makefile, golangci-lint, GitHub Actions CI, a throwaway `hello` gRPC service proving
the skeleton end to end.

**Out of scope:** any business logic, database schemas, Kafka/RabbitMQ helpers beyond header carriers (they come with their first user).

## Tasks

- [x] `go.work` with modules `pkg`, `gen/go`, `tests`, `services/hello` (deleted at end of Phase 01). `tools` kept outside the workspace (see risks)
- [x] `tools/go.mod` with `tool` directives: `buf`, `sqlc`, `goose`, `golangci-lint`, `protoc-gen-go`, `protoc-gen-go-grpc`
- [x] `buf.yaml` (v2, lint `STANDARD`, breaking `WIRE_JSON`), `buf.gen.yaml` → `gen/go`
- [x] `proto/quotify/hello/v1/hello.proto` (temporary) → generated code committed
- [x] `pkg/config`: typed env loading with validation
- [x] `pkg/logx`: JSON `slog` handler adding `service`, `version`, `trace_id`, `span_id`. Redaction `LogValuer`
- [x] `pkg/otelx`: tracer + meter provider init (OTLP gRPC), W3C propagator, `KafkaHeadersCarrier`, `AMQPTableCarrier` + unit tests
- [x] `pkg/grpcx`: server/client constructors with `otelgrpc`, recovery, deadline defaults, health service, reflection in local env
- [x] `pkg/health`: `/healthz`, `/readyz` with pluggable checks. `/metrics` handler
- [x] `pkg/idgen`: UUIDv7, ULID, UUIDv5 helpers
- [x] `pkg/testkit`: testcontainers helpers for Postgres, Redpanda, RabbitMQ, Redis (start, DSN, cleanup)
- [x] `deploy/compose/compose.yaml`: postgres (init SQL creating all DBs and roles), redpanda + console, rabbitmq, redis, clickhouse, minio, mailpit; `obs` profile: otel-collector, prometheus, tempo, grafana
- [x] `deploy/otel/collector.yaml`, `deploy/prometheus/prometheus.yml`, Grafana datasource provisioning
- [x] Makefile targets: `tools gen lint test test-integration up up-infra down clean`
- [x] `.golangci.yml` (govet, staticcheck, errcheck, gosec, revive, depguard placeholder, forbidigo placeholder)
- [x] `.github/workflows/ci.yml`: lint, buf lint + breaking, unit, integration (Docker), `make gen` diff check
- [x] `services/hello`: gRPC `SayHello`, which produces a record to a test Kafka topic with trace headers and consumes it back
- [x] Dockerfile template (multi-stage, distroless)
- [x] `.env.example`, `CONTRIBUTING.md` (branching, Conventional Commits)

## Deliverables

- Running local stack (`make up-infra`) with all UIs reachable (Redpanda Console, RabbitMQ, Mailpit, Grafana, MinIO).
- CI pipeline on GitHub Actions.
- `hello` service demonstrating gRPC + Kafka header propagation with a trace in Tempo.

## Acceptance criteria

- [x] `make up-infra` brings every container to healthy within 90 s on a laptop. A compose healthcheck exists for each
- [ ] `make gen && git diff --exit-code` passes in CI
- [ ] `make lint test` passes. CI is green on `main`
- [x] Integration test: `testkit` starts Postgres + Redpanda and the `hello` round trip passes
- [x] Unit test: `KafkaHeadersCarrier` and `AMQPTableCarrier` round-trip `traceparent`
- [ ] Manual check, documented with a screenshot in the PR: one trace in Tempo spanning `SayHello` → Kafka produce → consume

## Dependencies

None.

## Risks / open questions

- Memory footprint of the full compose stack (ClickHouse + Redpanda + RabbitMQ). Mitigation: profiles and memory limits per container.
- Generated code committed vs. generated in CI: decided **committed** ([11-repo-layout.md](../11-repo-layout.md)). Revisit if diffs get noisy.
- Go 1.27 `tool` directive support in all tools: works for all six (`go build tool` into `./bin`).
- **Changed while building:** `tools/` is not part of `go.work`. Its dependency graph would otherwise raise versions for every service module.
- **Changed while building:** MinIO images are no longer on Docker Hub/Quay. Chainguard's build is used, pinned by digest ([ADR 0013](../adr/0013-s3-compatible-object-storage.md)).
- Host ports 5432/8080 often clash with other local stacks: overridable via `QUOTIFY_PG_HOST_PORT` / `QUOTIFY_CONSOLE_HOST_PORT`. hello uses 7099 (7000 is macOS AirPlay).
