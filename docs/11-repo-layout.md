# 11 — Repository layout (planned)

Go workspace monorepo ([ADR 0001](adr/0001-go-workspace-monorepo.md)). Each service is its own module,
so dependencies and builds stay independent. Shared code lives in a small set of library modules.

```
quotify/
├── go.work                         # use ./pkg ./gen/go ./services/* ./tests (tools/ is outside: see 10-local-dev)
├── Makefile
├── buf.yaml / buf.gen.yaml         # buf v2 config, lint + breaking rules
├── proto/
│   └── quotify/
│       ├── events/v1/              # UsageEvent, LimitExceeded, EntitlementState, InvoiceFact
│       ├── commands/v1/            # CommandEnvelope, ReplyEnvelope and payloads
│       ├── plans/v1/  ingest/v1/  aggregator/v1/  limits/v1/  billing/v1/  analytics/v1/
│       └── testdata/               # golden binary fixtures per release
├── gen/go/                         # module github.com/0furkancolak/quotify/gen/go — generated, committed
├── pkg/                            # module github.com/0furkancolak/quotify/pkg
│   ├── config/                     # env parsing
│   ├── logx/                       # slog setup, trace-aware handler, redaction
│   ├── otelx/                      # SDK init, Kafka + AMQP header carriers
│   ├── grpcx/                      # server/client constructors, interceptors (tenant, recovery, otel)
│   ├── tenant/                     # tenant context type, metadata keys
│   ├── pgxx/                       # pools (app/system), InTenantTx, migrations runner
│   ├── outbox/                     # generic outbox table contract + relay (Kafka and AMQP sinks)
│   ├── kafkax/                     # franz-go client factory, DB-offset consumer, DLQ producer, redrive filter
│   ├── rabbitx/                    # connection mgmt, topology, publisher confirms, retry/DLX helpers, worker skeleton
│   ├── period/                     # period ids, cutoff, Classify() — shared by aggregator + limits
│   ├── money/                      # Amount (int64 minor), decimal unit prices, rounding, currency exponent table
│   ├── quantity/                   # exact decimal ↔ scaled int64 units (per-meter scale)
│   ├── blob/                       # S3 API client (Put/Head/PresignGet), works with MinIO and any S3
│   ├── payments/                   # PaymentProvider interface + environment routing (sandbox ⇒ mockpsp)
│   ├── idgen/                      # UUIDv7, ULID, uuidv5 helpers
│   ├── health/                     # /healthz /readyz
│   └── testkit/                    # testcontainers helpers, fixtures, fake clock
├── services/
│   ├── gateway/
│   │   ├── go.mod                  # module github.com/0furkancolak/quotify/services/gateway
│   │   ├── cmd/gateway/main.go
│   │   └── internal/{http,auth,ratelimit,upstream}/
│   ├── plans-svc/
│   │   ├── cmd/plans-svc/main.go
│   │   ├── internal/{grpc,domain,store,store/queries,relay}/
│   │   ├── migrations/             # goose SQL
│   │   └── sqlc.yaml
│   ├── ingest-svc/                 # same shape + internal/relay
│   ├── aggregator/                 # + internal/consumer, internal/replay
│   ├── limits-svc/                 # + internal/consumer, internal/reconcile, lua/
│   ├── billing-svc/                # + internal/{scheduler,rating,saga,sweeper,rerating}
│   ├── invoice-worker/             # + internal/render, templates/
│   ├── payments-worker/            # + internal/psp
│   ├── notifier/                   # + internal/{smtp,templates}
│   ├── assets-svc/                 # + internal/{upload,sweeper,verifier}
│   └── analytics-svc/              # + internal/{consumer,chquery}, migrations/ (ClickHouse SQL)
├── tools/
│   ├── go.mod                      # pinned dev tools (tools directive)
│   ├── mockpsp/                    # fake payment provider (HTTP)
│   ├── quotifyctl/                 # operator CLI: dlq, replay, rerate, close-period, seed
│   └── eventgen/                   # load + correctness generator (shared with tests/chaos)
├── tests/
│   ├── go.mod
│   ├── e2e/
│   ├── chaos/                      # chaosmonkey, assertions, report
│   └── load/                       # k6 scripts
├── deploy/
│   ├── compose/                    # compose.yaml, init SQL, .env.example
│   ├── rabbitmq/definitions.json
│   ├── grafana/{dashboards,provisioning}/
│   ├── prometheus/{prometheus.yml,rules/}
│   ├── otel/collector.yaml
│   ├── kind/cluster.yaml
│   └── helm/quotify/               # umbrella chart, one subchart per service (incl. mock-psp)
│       ├── values-kind.yaml        # sandbox + live tenants, in-cluster MinIO
│       └── values-sandbox.yaml     # sandbox-only deployment (namespace quotify-sandbox)
├── .github/workflows/              # ci.yml, e2e.yml, nightly.yml, release.yml
└── docs/
```

## Rules

- `internal/` everywhere. Services never import another service's packages. They share only `pkg/` and `gen/go/`.
- `pkg/` must stay small and generic. Business logic doesn't belong there. `period` and `money` are the deliberate
  exceptions: they're shared *definitions*, and two implementations of lateness would be a correctness bug.
- Generated code (`gen/go`, `sqlc` output in `internal/store/sqlc`) **is committed**, so `go build` works without tools
  and diffs are reviewable. CI fails if `make gen` produces a diff.
- Dockerfiles: one multi-stage `services/<svc>/Dockerfile` with a `distroless/static` final image. The build context is the repo root,
  so `go.work` resolves.
- Import-boundary lint (`depguard`): only `internal/worker`, `internal/relay`, `internal/consumer`, `internal/scheduler` may receive
  the `system` Postgres pool ([06-multi-tenancy.md](06-multi-tenancy.md#roles-per-service)).
