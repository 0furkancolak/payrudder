# 10 — Local development

## Two commands

| Command | When | What it does |
|---|---|---|
| `make setup` | once per machine (and after `make clean`) | builds the pinned tools, starts the **infrastructure containers only** (Postgres, Redpanda, RabbitMQ, Redis, ClickHouse, MinIO, Mailpit), creates the topics and buckets, installs the console's packages |
| `make dev` | every day | runs the Go services **on your machine** (`quotifyctl dev`) and the console (Vite, port 3001), all with reload; Ctrl-C stops everything. `make dev ONLY=gateway,plans-svc` or `SKIP=analytics-svc` runs some |

The services' settings are the compose file's, read as `docker compose config`, with each container address turned into the host port the container publishes (`postgres:5432` becomes `localhost:42432`, `dns:///plans-svc:7001` becomes `localhost:42701`, Redpanda's `9092` becomes its external `19092`): one list of settings, not one for Docker and one for the host.
A service is rebuilt and restarted when a `.go`, `.sql` or `.yaml` file of its module or of `pkg/` changes; its log lines carry its name in colour. Tracing is off unless the observability profile is up. If another project holds port 5432, start with `QUOTIFY_PG_HOST_PORT=5440 make setup dev`.
Application images exist for CI and releases; `make dev-docker` builds and runs them when you want to check an image. `make mem-report` shows what every running container holds.

The sections below describe the compose stack in full.

## Default dev seed

`make dev` fills a **local** stack once with sample data, so that every screen of the product app has something to show. It runs in the background after the gateway and plans-svc answer (`quotifyctl dev` starts it; the first run on an empty database takes a few minutes because it closes a sandbox period with the real workers), prints `dev-seed:` lines, and **never fails or blocks `make dev`**: if it cannot finish (a service is still compiling, Mailpit is down) it logs a `WARNING` and tries again up to three times; then `make dev-seed` runs it by hand. `SEED=0 make dev` (or `quotifyctl dev --no-seed`) skips it.

| Who | E-mail | Password | Account | Apps (sandbox tenant is filled) |
|---|---|---|---|---|
| Main dev user | `user1@example.com` | `password123` | Acme Labs | Storefront (TRY, Europe/Istanbul), Mobile Game (USD, UTC) |
| Second user | `user2@example.com` | `password123` | Beta Studio | Notes Sync (EUR, Europe/Berlin): a tiny app, to look at isolation (user2 sees nothing of Acme Labs, and the other way round) |

**These credentials are public and for local development only.** The seed refuses to run unless the gateway is `localhost`, `127.0.0.1`, `[::1]` or `*.localhost` (`--i-know-this-is-dev` lets a dev stack on another host name through) and never when `QUOTIFY_ENV` is `prod`, `production`, `live` or `staging`. No Helm values, Railway script or compose profile runs it, and the sign-in page shows the credentials only in the Vite dev server (`import.meta.env.DEV`), never in a production build.

What is seeded, per app, in the **sandbox** environment (the live environment is left empty; the dashboard's Sandbox switch shows the data, and the organisation home cards show the live numbers, so they read zero):

- two meters (`api_calls`, `storage_gb` with three decimals) and three packages (flat, flat + usage, usage only with tiers), entitlements with alert thresholds;
- 6 to 7 customers, one per sandbox payment scenario (`success`, `declined`, `insufficient_funds_once`, `error_then_success`, none on file), each subscribed, one billed in advance;
- usage events for each past day of the current month, then one sandbox period close: finalized invoices that are paid or open with a declined payment (dunning on seconds), the payments in the ledger;
- two credit notes (a partial refund to the card, a credit to the balance), a credit pack, customers with a credit balance and spent credits, the low-balance threshold;
- three payment links (ad hoc, credit pack, an open invoice);
- 8 to 9 expenses in two to three currencies, several recurring, so the accounting summary has income, refunds, expenses and profit per currency;
- a webhook endpoint (an address that does not exist, so its deliveries fail and the health card asks for attention).

How it works and why it is safe to run again: it uses the public API only (a session with `X-Quotify-Tenant` for everything the product API allows, the operator token only for credit notes, and the Mailpit mail of each seeded person to prove the address, the way a person would). Every object is looked up by a stable name or key first (customers `dev_cus_NN`, usage keys `devseed:<day>:<customer>:<meter>`, grants and payment links by idempotency key, expenses by vendor and currency), so a second run changes nothing and a missing piece (an expense you voided) is put back. A person who already exists with another password is never touched: the run stops and says so. Each new day a re-run adds that day's usage events, nothing else.

To start over, wipe the databases and seed again: `make clean` (removes the volumes), then `make setup` and `make dev`.

## Signing in locally (three methods, and the operator)

The sign-in page at <http://localhost:42301/signin> offers the three methods of [phase 42](phases/phase-42-signin-methods.md):

- **Password.** `user1@example.com` / `password123` after `make dev` (the dev seed).
- **An e-mailed code.** Choose the *Email code* tab, type any address (a new one gets an account when the code is entered), and open Mailpit at <http://localhost:42825>: the mail has the six digits. A code lives 10 minutes, works once, and is burned after five wrong tries; a new one can be asked for after a minute. `QUOTIFY_OTP_PEPPER` (the compose file gives plans-svc a local default) keys the hash of the code.
- **Google.** The button is hidden until `QUOTIFY_GOOGLE_CLIENT_ID` and `QUOTIFY_GOOGLE_CLIENT_SECRET` are set on the gateway. To try it without Google: `docker compose -f deploy/compose/compose.yaml --profile oidc up -d mockoidc`, then run the gateway with `QUOTIFY_GOOGLE_CLIENT_ID=mock-client-id QUOTIFY_GOOGLE_CLIENT_SECRET=mock-client-secret QUOTIFY_GOOGLE_ISSUER=http://localhost:42899` (`make dev` reads the compose file's environment, so export them before it; in compose itself the issuer is `http://mockoidc:8099`). The stand-in at <http://localhost:42899> asks for any address. A real Google client: [docs/13-admin-ui.md](13-admin-ui.md#sign-in-methods).

**The operator console** has no sign-in of its own and no token form. `QUOTIFY_OPERATOR_EMAILS` on the gateway lists the people who are operators; the compose file defaults it to `user1@example.com`, so after signing in as the dev user the side bar shows the operator console (`/operator`: tenants, playground, dead letters). Change the list with `QUOTIFY_OPERATOR_EMAILS=you@example.com make dev` (the address must be verified: a code sign-in verifies it). `QUOTIFY_OPERATOR_TOKEN` (default `local-operator-token-change-me-0123456789`) is the **machine** credential only: `quotifyctl`, `make seed` / `demo`, the end-to-end tests and the mock services use it against the API; it cannot be entered in the UI.


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
| postgres | `postgres:17.11-alpine` | 42432 (`QUOTIFY_PG_HOST_PORT`) | one instance. Init script creates `plans_db`, `ingest_db`, `aggregator_db`, `billing_db`, `payments_db`, `notifier_db`, `assets_db`, `mockpsp_db` and the per-service roles |
| redpanda | `redpandadata/redpanda` | 42092 (Kafka), 42081 (schema registry, unused), 42644 (admin) | single node, `--mode dev-container` |
| redpanda-console | `redpandadata/console` | 42080 (`QUOTIFY_CONSOLE_HOST_PORT`) | topic browser |
| rabbitmq | `rabbitmq:4-management` | 42672, 42673 (UI) | `definitions.json` loaded at boot |
| redis | `redis:7` | 42379 | `appendonly yes`, `appendfsync everysec` |
| clickhouse | `clickhouse/clickhouse-server` | 42123 (HTTP), 42900 (native) | |
| minio | `cgr.dev/chainguard/minio@sha256:…` | 42901 (S3 API), 42902 (console) | S3-compatible store ([ADR 0013](adr/0013-s3-compatible-object-storage.md)). Bucket `invoices` + sandbox lifecycle rule created by an init container (`mc`) |
| mailpit | `axllent/mailpit` | 42025 (SMTP), 42825 (UI) | catches tenants' customer mail (notifier) and the mail to people from plans-svc: verification, reset and invitation links |
| toxiproxy | `ghcr.io/shopify/toxiproxy` | 42474 (API) | `profile: chaos` only |

### Observability (`profile: obs`)

| Service | Ports |
|---|---|
| otel-collector | 42317 (OTLP gRPC), 42318 (OTLP HTTP) |
| prometheus | 42990 |
| tempo | 42200 |
| loki | internal only (Grafana queries it; Vector ships logs to it) |
| grafana | 42300 (anonymous admin locally, dashboards provisioned) |

### Payrudder services (`profile: app`)

| Service | gRPC | HTTP | Metrics/health |
|---|---|---|---|
| gateway | — | **42800** (public API) | 42910 |
| plans-svc | 42701 | — | 42911 |
| ingest-svc | 42702 | — | 42912 |
| aggregator | 42703 | — | 42913 |
| limits-svc | 42704 | — | 42914 |
| billing-svc | 42705 | — | 42915 |
| analytics-svc | 42706 | — | 42916 |
| invoice-worker | — | — | 42917 |
| payments-worker | — | 42895 (internal API, used by the gateway) | 42918 |
| notifier | — | — | 42919 |
| assets-svc | 42707 | — | 42921 |
| mock-psp | — | 42890 | 9110 | (sandbox payment provider, also used for live tenants in the local demo)

Every service exposes `/healthz` (liveness), `/readyz` (readiness: DB, broker reachable, partitions assigned) and `/metrics`
on its metrics port.

## Configuration

Environment variables only (12-factor), parsed by `pkg/config` into typed structs (`caarlos0/env`). Every `QUOTIFY_*` variable can also be written `PAYRUDDER_*` (for example `PAYRUDDER_LOG_LEVEL=debug`); when both are set, `PAYRUDDER_*` wins ([ADR 0021](adr/0021-payrudder-surface-rename.md)). The examples below use the original prefix, which keeps working. Example:

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
| `make dev-seed` | Run the default dev seed again by hand (see [Default dev seed](#default-dev-seed)); idempotent, local stacks only |
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
