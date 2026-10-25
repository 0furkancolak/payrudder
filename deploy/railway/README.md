# Quotify on Railway

Everything to run the stack on [Railway](https://railway.com): a config-as-code file per service, images for the backing services Railway has no plugin
for, and a script that drives the Railway CLI. The design and the decisions are in [phase 31](../../docs/phases/phase-31-railway.md).

> **Status: written without a Railway account.** Nothing here has been deployed. What *was* checked, and what was not, is listed under
> [What is verified](#what-is-verified). Read that before you rely on it.

## Topology

One Railway project, one environment. Every service talks to the others on the private network (`<service>.railway.internal`); only three are public.

| Service | Source | Public | Volume | Kind |
|---|---|---|---|---|
| `Postgres`, `Redis` | Railway plugins | no | plugin | backing |
| `redpanda` | `images/redpanda` | no | `/var/lib/redpanda/data` | backing (Kafka) |
| `rabbitmq` | `images/rabbitmq` | no | `/var/lib/rabbitmq` | backing |
| `minio` | `images/minio` | **yes** (presigned links) | `/data` | backing (S3) |
| `clickhouse` | `images/clickhouse` | no | `/var/lib/clickhouse` | backing, full profile only |
| `mailpit` | `images/mailpit` | no | none | mail catcher |
| `init-postgres`, `init-redpanda`, `init-minio` | `images/*-init` | no | none | one-shot jobs (databases and roles, topics, buckets) |
| `plans-svc` `ingest-svc` `aggregator` `limits-svc` `billing-svc` `assets-svc` `payments-worker` `invoice-worker` `notifier` `mockpsp` | `go.Dockerfile` | no | none | Quotify |
| `analytics-svc` | `go.Dockerfile` | no | none | Quotify, full profile only |
| `gateway` | `go.Dockerfile` | **yes** | none | Quotify |
| `admin-ui` | `admin.Dockerfile` | **yes** | none | the operator console |

Service names are the compose names, lower-case, because they become DNS names. Do not rename them: the variables below refer to them.

## How it differs from the compose stack

- **`PORT`.** Railway health-checks and routes public traffic to the port in `PORT`. `pkg/config` honours it without changing the YAML/env contract: `PORT`
  fills `QUOTIFY_HTTP_ADDR` (gateway), `MOCKPSP_HTTP_ADDR` (mock-psp) or else `QUOTIFY_METRICS_ADDR`, unless that variable is set itself. So for the gRPC
  services and the workers `PORT` is the ops port that serves `/healthz`, `/readyz`, `/metrics`; their gRPC port stays fixed (7001 to 7007) and is reached
  on the private network. The gateway also answers `GET /healthz` on its public port (liveness only; `/metrics` stays on its private port 9100).
  `bootstrap.sh` sets `PORT` explicitly on every service (the tables below) so the addresses other services use are fixed.
- **IPv6.** Railway's private network is IPv6 (newer environments are dual-stack). Go's `:port` listeners are dual-stack, so every Quotify listener works
  as it is. The console (`admin-ui`, `vite preview`) listens on `::` too, RabbitMQ and ClickHouse are configured for `::`, Redpanda listens on `REDPANDA_BIND`
  (default `0.0.0.0`; set it to `::` if your environment is IPv6-only).
- **`QUOTIFY_ENV=railway`**, a new accepted value (it only labels traces and keeps gRPC reflection off).
- **Dockerfiles.** Railway rejects BuildKit cache mounts without its own `id`, so the services build from `go.Dockerfile`, which is the per-service
  Dockerfile minus the cache mounts and with the service directory as the build argument `SERVICE_DIR` (a service variable).
- **Migrations** run at start (`QUOTIFY_MIGRATE_ON_START=true`, one replica of each service), not in a Job. See [Migrations](#migrations).
- **Postgres** is one managed instance with a database and three roles per service, created by `init-postgres` (the same layout as
  `deploy/compose/postgres/01-databases.sh`). The role passwords are shared by service: three secrets, `PG_MIGRATOR_PASSWORD`, `PG_APP_PASSWORD`,
  `PG_SYSTEM_PASSWORD`, held as variables of `init-postgres` and referenced as `${{init-postgres.PG_APP_PASSWORD}}`. A role can only connect to its own
  service's database, so sharing a password across services does not cross the service boundary. This needs a Postgres user that may
  `CREATE ROLE ... BYPASSRLS`: Railway's `postgres` user is a superuser. On a managed Postgres that is not (Neon, Supabase, RDS without
  `rds_superuser`), create the 24 roles yourself the way the script does.

## Prerequisites

- A Railway project on a plan that allows the memory below, and the [CLI](https://docs.railway.com/cli) (`brew install railway`), `openssl`, `bash`.
- `railway login`, then in an empty directory `railway init` (or `railway link` to an existing project).
- For `--source github`: the repository on GitHub and Railway's GitHub app installed for it.

## Quick path

```bash
deploy/railway/bootstrap.sh --dry-run --repo you/quotify            # read what it will do
deploy/railway/bootstrap.sh --repo you/quotify                      # full profile
deploy/railway/bootstrap.sh --repo you/quotify --profile small      # without ClickHouse and analytics-svc
deploy/railway/bootstrap.sh --source local                          # deploy this checkout with `railway up` instead of from GitHub
```

It creates the plugins and services, volumes, variables (generating the secrets once, never overwriting one that exists), the three public domains,
and deploys in order: backing services, the three init jobs, `plans-svc`, the rest, `gateway` and `admin-ui`, pausing `--wait` seconds (default 45)
between stages. `make railway-check` runs the static tests and a dry run.

The CLI cannot set a service's **config-as-code path**. For each service open *Settings → Config-as-code* and enter the path (the script prints the list):
`/deploy/railway/services/<name>.railway.json` for the Quotify services, `/deploy/railway/infra/<name>.railway.json` for the rest. That is what gives
each service its health check, restart policy and watch paths. Until then it still builds (`RAILWAY_DOCKERFILE_PATH` is set) but is not health-checked.

## By hand (what the script does)

1. **Project.** Create it; add the **Postgres** and **Redis** plugins from *New → Database*. Keep their default names.
2. **Services.** For each name in the topology table: *New → Empty service*, name it exactly, and in *Settings* set the config-as-code path (above).
   Build from the same repository; the root directory stays `/`.
3. **Volumes.** Attach the volume at the mount path from the table to `redpanda`, `rabbitmq`, `minio`, `clickhouse`. Set `RAILWAY_RUN_UID=0` on those
   four (Railway volumes are root-owned).
4. **Variables.** Set the tables below. Secrets are generated once and only ever live in Railway:

   | Secret | Held by | Generate with |
   |---|---|---|
   | `PG_MIGRATOR_PASSWORD`, `PG_APP_PASSWORD`, `PG_SYSTEM_PASSWORD` | `init-postgres` | `openssl rand -hex 24` (hex: they go into URLs) |
   | `RABBITMQ_PASSWORD` | `rabbitmq` | `openssl rand -hex 24` |
   | `MINIO_ROOT_PASSWORD` | `minio` | `openssl rand -hex 24` |
   | `CLICKHOUSE_PASSWORD` | `clickhouse` | `openssl rand -hex 24` |
   | `QUOTIFY_OPERATOR_TOKEN` | `gateway` | `openssl rand -hex 32` (at least 32 characters; the API's machine credential) |
   | `QUOTIFY_OTP_PEPPER` | `plans-svc` | `openssl rand -hex 32` (keys the hash of one-time sign-in codes) |
   | `PAYMENTS_INTERNAL_TOKEN` | `payments-worker` | `openssl rand -hex 32` |
   | `QUOTIFY_SECRETS_KEY` | `payments-worker` | `openssl rand -base64 32` (exactly 32 bytes; seals tenants' provider credentials and webhook secrets) |

   Losing `QUOTIFY_SECRETS_KEY` makes every sealed credential unreadable: back it up outside Railway.
5. **Domains.** *Settings → Networking → Generate domain* on `gateway` (port 8000), `admin-ui` (3001) and `minio` (9000). Custom domains:
   `railway domain api.example.com --service gateway --port 8000`, then the DNS records it prints; do the same for the console. If you add a custom
   domain to the gateway, set `QUOTIFY_PUBLIC_URL=https://api.example.com` on it (hosted invoice links start with it), and to MinIO, change
   `S3_PUBLIC_ENDPOINT` on `billing-svc` and `assets-svc`.
6. **Deploy in order**, waiting for each stage to be healthy: backing services; the three `init-*` jobs (each ends *Completed*); `plans-svc`;
   `ingest-svc`, `aggregator`, `limits-svc`, `assets-svc`, `mockpsp`, `analytics-svc`; `billing-svc`, `payments-worker`, `invoice-worker`, `notifier`;
   `gateway`, `admin-ui`. A service started too early restarts (`ON_FAILURE`, 10 tries) until its dependencies answer, so a wrong order is slow, not fatal.

## Memory

Railway bills by resident memory. Every Go service gets `GOMEMLIMIT=96MiB` (override with `GO_MEMLIMIT=128MiB deploy/railway/bootstrap.sh ...`), and
`pkg/memlimit` defaults `GOGC` to 50, so a service that holds a few MiB of live data stays near that instead of growing to twice it. Database pools
are 4 connections each (`pool_max_conns` in a DSN raises it) and idle ones close after a minute. Measured on the compose stack, idle services sit at 4 to 26 MiB and 12 to 60 MiB under the e2e load.

## Environment variables

`PORT`, `QUOTIFY_ENV` and `QUOTIFY_SERVICE_NAME` are set on every Quotify service (`railway`, the service name). `SERVICE_DIR` and
`RAILWAY_DOCKERFILE_PATH` (`deploy/railway/go.Dockerfile`) are the build settings of the Go services. Values written `${{x.Y}}` are Railway reference
variables; `<role>` below is `app`, `system` or `migrator`. The names here are the ones the services read; the defaults are in each
`cmd/*/config.default.yaml`.

Shorthand: **PGDSN(`db`, role)** = `postgres://<db>_<role>:${{init-postgres.PG_<ROLE>_PASSWORD}}@${{Postgres.RAILWAY_PRIVATE_DOMAIN}}:5432/<db>_db`,
**KAFKA** = `redpanda.railway.internal:9092`, **AMQP** = `amqp://quotify:${{rabbitmq.RABBITMQ_PASSWORD}}@rabbitmq.railway.internal:5672/`.

### gateway

| Variable | Value | Note |
|---|---|---|
| `QUOTIFY_SERVICE_NAME` | `gateway` | |
| `PORT` | `8000` | the HTTP API; the public domain's port |
| `QUOTIFY_OPERATOR_TOKEN` | generated | at least 32 characters; the machine credential of the operator API (scripts, `quotifyctl`); never typed into the console |
| `QUOTIFY_OPERATOR_EMAILS` | e.g. `you@example.com` | comma separated; people who sign in with one of these and have verified it are platform operators (empty: nobody) |
| `QUOTIFY_TRUSTED_PROXY_SECRET` | generated, 32+ characters | only with the console on Cloudflare: the same value as the Worker secret `EDGE_AUTH_SECRET`; makes the gateway count sign-in limits per visitor ([runbook](../../docs/runbooks/console-first-deploy.md)) |
| `QUOTIFY_APP_URL` | `https://${{admin-ui.RAILWAY_PUBLIC_DOMAIN}}` | where the browser returns after Google; also set on `plans-svc` for the links in mails |
| `QUOTIFY_GOOGLE_CLIENT_ID`, `QUOTIFY_GOOGLE_CLIENT_SECRET` | from Google | optional: both set shows "Continue with Google" ([how](../../docs/13-admin-ui.md#sign-in-methods)); register `<console domain>/gw/auth/google/callback` as the redirect URI |
| `QUOTIFY_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `QUOTIFY_INGEST_ADDR` | `dns:///ingest-svc.railway.internal:7002` | |
| `QUOTIFY_AGGREGATOR_ADDR` | `dns:///aggregator.railway.internal:7003` | |
| `QUOTIFY_LIMITS_ADDR` | `dns:///limits-svc.railway.internal:7004` | |
| `QUOTIFY_BILLING_ADDR` | `dns:///billing-svc.railway.internal:7005` | |
| `QUOTIFY_ANALYTICS_ADDR` | `dns:///analytics-svc.railway.internal:7006` | full profile only |
| `QUOTIFY_ASSETS_ADDR` | `dns:///assets-svc.railway.internal:7007` | |
| `QUOTIFY_REDIS_URL` | `${{Redis.REDIS_URL}}` | |
| `QUOTIFY_ALLOWED_ENVIRONMENTS` | `sandbox,live` | `sandbox` alone refuses live accounts |
| `QUOTIFY_PAYMENTS_URL` | `http://payments-worker.railway.internal:8095` | |
| `QUOTIFY_PAYMENTS_INTERNAL_TOKEN` | `${{payments-worker.PAYMENTS_INTERNAL_TOKEN}}` | |
| `QUOTIFY_PUBLIC_URL` | `https://${{gateway.RAILWAY_PUBLIC_DOMAIN}}` | |

### plans-svc

| Variable | Value | Note |
|---|---|---|
| `QUOTIFY_SERVICE_NAME` | `plans-svc` | |
| `PORT` | `9100` | ops port (health) |
| `QUOTIFY_PG_APP_DSN` | PGDSN(`plans`, app) | |
| `QUOTIFY_PG_SYSTEM_DSN` | PGDSN(`plans`, system) | |
| `QUOTIFY_PG_MIGRATOR_DSN` | PGDSN(`plans`, migrator) | used when `QUOTIFY_MIGRATE_ON_START` is true |
| `QUOTIFY_MIGRATE_ON_START` | `true` | |
| `QUOTIFY_REDIS_URL` | `${{Redis.REDIS_URL}}` | |
| `QUOTIFY_KAFKA_BROKERS` | KAFKA | |
| `QUOTIFY_SECRETS_KEY` | `${{payments-worker.QUOTIFY_SECRETS_KEY}}` | the same key as payments-worker's |
| `QUOTIFY_ALLOWED_ENVIRONMENTS` | `sandbox,live` | |

`QUOTIFY_WEBHOOK_ALLOW_PRIVATE` stays at its default `false`: tenants' webhook endpoints may not point at the private network.

### ingest-svc

| Variable | Value | Note |
|---|---|---|
| `QUOTIFY_SERVICE_NAME` | `ingest-svc` | |
| `PORT` | `9100` | |
| `QUOTIFY_PG_APP_DSN` | PGDSN(`ingest`, app) | |
| `QUOTIFY_PG_SYSTEM_DSN` | PGDSN(`ingest`, system) | |
| `QUOTIFY_PG_MIGRATOR_DSN` | PGDSN(`ingest`, migrator) | |
| `QUOTIFY_MIGRATE_ON_START` | `true` | |
| `QUOTIFY_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `QUOTIFY_KAFKA_BROKERS` | KAFKA | |

### aggregator

| Variable | Value | Note |
|---|---|---|
| `QUOTIFY_SERVICE_NAME` | `aggregator` | |
| `PORT` | `9100` | |
| `QUOTIFY_PG_APP_DSN` | PGDSN(`aggregator`, app) | |
| `QUOTIFY_PG_SYSTEM_DSN` | PGDSN(`aggregator`, system) | |
| `QUOTIFY_PG_MIGRATOR_DSN` | PGDSN(`aggregator`, migrator) | |
| `QUOTIFY_MIGRATE_ON_START` | `true` | |
| `QUOTIFY_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `QUOTIFY_KAFKA_BROKERS` | KAFKA | |

### limits-svc

| Variable | Value | Note |
|---|---|---|
| `QUOTIFY_SERVICE_NAME` | `limits-svc` | |
| `PORT` | `9100` | |
| `QUOTIFY_REDIS_URL` | `${{Redis.REDIS_URL}}` | |
| `QUOTIFY_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `QUOTIFY_AGGREGATOR_ADDR` | `dns:///aggregator.railway.internal:7003` | |
| `QUOTIFY_KAFKA_BROKERS` | KAFKA | |

### billing-svc

| Variable | Value | Note |
|---|---|---|
| `QUOTIFY_SERVICE_NAME` | `billing-svc` | |
| `PORT` | `9100` | |
| `QUOTIFY_PG_APP_DSN` | PGDSN(`billing`, app) | |
| `QUOTIFY_PG_SYSTEM_DSN` | PGDSN(`billing`, system) | |
| `QUOTIFY_PG_MIGRATOR_DSN` | PGDSN(`billing`, migrator) | |
| `QUOTIFY_MIGRATE_ON_START` | `true` | |
| `QUOTIFY_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `QUOTIFY_INGEST_ADDR` | `dns:///ingest-svc.railway.internal:7002` | |
| `QUOTIFY_AGGREGATOR_ADDR` | `dns:///aggregator.railway.internal:7003` | |
| `QUOTIFY_KAFKA_BROKERS` | KAFKA | |
| `QUOTIFY_RABBITMQ_URL` | AMQP | |
| `S3_ENDPOINT` | `http://minio.railway.internal:9000` | |
| `S3_PUBLIC_ENDPOINT` | `https://${{minio.RAILWAY_PUBLIC_DOMAIN}}` | what a browser opens for a presigned invoice link |
| `S3_BUCKET` | `invoices` | |
| `S3_ACCESS_KEY` | `${{minio.MINIO_ROOT_USER}}` | |
| `S3_SECRET_KEY` | `${{minio.MINIO_ROOT_PASSWORD}}` | |
| `S3_FORCE_PATH_STYLE` | `true` | |

### analytics-svc

Full profile only.

| Variable | Value | Note |
|---|---|---|
| `QUOTIFY_SERVICE_NAME` | `analytics-svc` | |
| `PORT` | `9100` | |
| `CLICKHOUSE_ADDR` | `clickhouse.railway.internal:9000` | |
| `CLICKHOUSE_DB` | `quotify` | |
| `CLICKHOUSE_USER` | `quotify` | |
| `CLICKHOUSE_PASSWORD` | `${{clickhouse.CLICKHOUSE_PASSWORD}}` | |
| `QUOTIFY_KAFKA_BROKERS` | KAFKA | |

### assets-svc

| Variable | Value | Note |
|---|---|---|
| `QUOTIFY_SERVICE_NAME` | `assets-svc` | |
| `PORT` | `9100` | |
| `QUOTIFY_PG_APP_DSN` | PGDSN(`assets`, app) | |
| `QUOTIFY_PG_SYSTEM_DSN` | PGDSN(`assets`, system) | |
| `QUOTIFY_PG_MIGRATOR_DSN` | PGDSN(`assets`, migrator) | |
| `QUOTIFY_MIGRATE_ON_START` | `true` | |
| `S3_ENDPOINT` | `http://minio.railway.internal:9000` | |
| `S3_PUBLIC_ENDPOINT` | `https://${{minio.RAILWAY_PUBLIC_DOMAIN}}` | presigned upload and download URLs |
| `S3_BUCKET` | `assets` | |
| `S3_ACCESS_KEY` | `${{minio.MINIO_ROOT_USER}}` | |
| `S3_SECRET_KEY` | `${{minio.MINIO_ROOT_PASSWORD}}` | |
| `S3_FORCE_PATH_STYLE` | `true` | |

### payments-worker

| Variable | Value | Note |
|---|---|---|
| `QUOTIFY_SERVICE_NAME` | `payments-worker` | |
| `PORT` | `9100` | its HTTP API (provider webhooks, operator endpoints) stays on `PAYMENTS_HTTP_ADDR` `:8095` |
| `QUOTIFY_PG_SYSTEM_DSN` | PGDSN(`payments`, system) | |
| `QUOTIFY_PG_MIGRATOR_DSN` | PGDSN(`payments`, migrator) | |
| `QUOTIFY_MIGRATE_ON_START` | `true` | |
| `QUOTIFY_RABBITMQ_URL` | AMQP | |
| `QUOTIFY_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `QUOTIFY_BILLING_ADDR` | `dns:///billing-svc.railway.internal:7005` | |
| `MOCKPSP_URL` | `http://mockpsp.railway.internal:8090` | sandbox tenants always use the mock provider |
| `QUOTIFY_DEMO` | `false` | `true` is [demo mode](#demo-mode) |
| `QUOTIFY_SECRETS_KEY` | generated | 32 bytes, base64 |
| `PAYMENTS_INTERNAL_TOKEN` | generated | shared with the gateway |

Leave `PAYMENTS_LIVE_PROVIDER` unset: live tenants then use the provider account they configure (iyzico, Paddle), never the mock.

### invoice-worker

| Variable | Value | Note |
|---|---|---|
| `QUOTIFY_SERVICE_NAME` | `invoice-worker` | |
| `PORT` | `9100` | |
| `QUOTIFY_RABBITMQ_URL` | AMQP | |
| `S3_ENDPOINT` | `http://minio.railway.internal:9000` | |
| `S3_BUCKET` | `invoices` | |
| `S3_ASSETS_BUCKET` | `assets` | |
| `S3_ACCESS_KEY` | `${{minio.MINIO_ROOT_USER}}` | |
| `S3_SECRET_KEY` | `${{minio.MINIO_ROOT_PASSWORD}}` | |
| `S3_FORCE_PATH_STYLE` | `true` | |

### notifier

| Variable | Value | Note |
|---|---|---|
| `QUOTIFY_SERVICE_NAME` | `notifier` | |
| `PORT` | `9100` | |
| `QUOTIFY_PG_SYSTEM_DSN` | PGDSN(`notifier`, system) | |
| `QUOTIFY_PG_MIGRATOR_DSN` | PGDSN(`notifier`, migrator) | |
| `QUOTIFY_MIGRATE_ON_START` | `true` | |
| `QUOTIFY_RABBITMQ_URL` | AMQP | |
| `QUOTIFY_KAFKA_BROKERS` | KAFKA | |
| `QUOTIFY_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `SMTP_ADDR` | `mailpit.railway.internal:1025` | see [Mail](#mail) |

### mockpsp

| Variable | Value | Note |
|---|---|---|
| `QUOTIFY_SERVICE_NAME` | `mockpsp` | |
| `PORT` | `8090` | its API; `/healthz` is on it |
| `QUOTIFY_PG_APP_DSN` | PGDSN(`mockpsp`, app) | |
| `QUOTIFY_PG_MIGRATOR_DSN` | PGDSN(`mockpsp`, migrator) | |
| `QUOTIFY_MIGRATE_ON_START` | `true` | |

### admin-ui

| Variable | Value | Note |
|---|---|---|
| `PORT` | `3001` | the public domain's port |
| `GATEWAY_URL` | `http://gateway.railway.internal:8000` | the console proxies `/gw/*` to it, so the browser needs no CORS |
| `QUOTIFY_DEMO` | `false` | shows the demo banner when `true` |
| `BRAND_NAME` | `Quotify` | |

`PREFILL_PLAYGROUND` stays `false`: it pre-fills the operator playground's form, so it is for local stacks only. No credential is ever served to the browser.

### Backing services

| Service | Variable | Value |
|---|---|---|
| `init-postgres` | `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD`, `PGDATABASE` | `${{Postgres.RAILWAY_PRIVATE_DOMAIN}}`, `5432`, `${{Postgres.PGUSER}}`, `${{Postgres.PGPASSWORD}}`, `${{Postgres.PGDATABASE}}` |
| `init-postgres` | `PG_MIGRATOR_PASSWORD`, `PG_APP_PASSWORD`, `PG_SYSTEM_PASSWORD` | generated |
| `init-redpanda` | `QUOTIFY_KAFKA_BROKERS` | KAFKA |
| `init-minio` | `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY` | `http://minio.railway.internal:9000`, `${{minio.MINIO_ROOT_USER}}`, `${{minio.MINIO_ROOT_PASSWORD}}` |
| `redpanda` | `PORT`, `RAILWAY_RUN_UID`, optional `REDPANDA_BIND`, `REDPANDA_MEMORY` | `9644` (health: the admin API), `0`, `0.0.0.0` or `::`, `512M` |
| `rabbitmq` | `RABBITMQ_PASSWORD`, `PORT`, `RAILWAY_RUN_UID` | generated, `15692` (health: Prometheus endpoint), `0` |
| `minio` | `MINIO_ROOT_USER`, `MINIO_ROOT_PASSWORD`, `PORT`, `RAILWAY_RUN_UID` | `quotify`, generated, `9000`, `0` |
| `clickhouse` | `CLICKHOUSE_DB`, `CLICKHOUSE_USER`, `CLICKHOUSE_PASSWORD`, `CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT`, `PORT`, `RAILWAY_RUN_UID` | `quotify`, `quotify`, generated, `1`, `8123`, `0` |
| `mailpit` | `PORT` | `8025` |

**Another S3** (Cloudflare R2, Tigris, AWS): skip `minio` and `init-minio`; on `billing-svc`, `assets-svc` and `invoice-worker` set `S3_ENDPOINT`,
`S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_REGION`, `S3_FORCE_PATH_STYLE` for that provider, remove `S3_PUBLIC_ENDPOINT` unless it differs from the endpoint,
and create the `invoices` and `assets` buckets yourself. **Another Kafka or RabbitMQ** (Upstash, CloudAMQP): change `QUOTIFY_KAFKA_BROKERS` or
`QUOTIFY_RABBITMQ_URL` and drop the service; the topics (`deploy/compose/redpanda/topics.sh`) and, for RabbitMQ, the exchanges and queues from
`deploy/rabbitmq/definitions.json` must exist (import the file in the provider's management UI).

## Migrations

Each service with a database applies its own migrations when it starts, as the schema owner (`QUOTIFY_MIGRATE_ON_START=true` and the `*_MIGRATOR_DSN`).
That is safe with one replica, which is what these configs run. Order that matters:

1. `init-postgres` must have *Completed* before any service starts (it creates the roles and databases the migrations need).
2. `init-redpanda` must have created the topics (Redpanda does not auto-create them).
3. `plans-svc` first, then the others: the rest call it. The order in `bootstrap.sh` is this one.

To take the migrator credential out of the running services afterwards, set `QUOTIFY_MIGRATE_ON_START=false` and remove `QUOTIFY_PG_MIGRATOR_DSN`
from each; set them again before an upgrade that carries migrations. With more than one replica, run the migration as its own deploy first (one replica),
then scale: Railway has no pre-deploy hook that can run a distroless image's `QUOTIFY_MIGRATE_ONLY` mode.

## First tenant

The operator token (for the API and `quotifyctl`; the console never asks for it) is `railway variable list --service gateway --kv | grep QUOTIFY_OPERATOR_TOKEN`. From a checkout:

```bash
cd tools/quotifyctl
go run . seed --gateway https://<gateway domain> --operator-token "$TOKEN" --currency TRY --timezone Europe/Istanbul --grace 24h
```

It creates an account with a sandbox and a live tenant, an API key for each (printed once) and a sample catalogue. Without `quotifyctl`, the call is
`POST /admin/v1/accounts` with `Authorization: Bearer <operator token>` ([api/openapi/v1.yaml](../../api/openapi/v1.yaml)). Open the console at the
`admin-ui` domain, sign up (or sign in with an e-mailed code) with an address you listed in `QUOTIFY_OPERATOR_EMAILS` (set it with `OPERATOR_EMAILS=you@example.com deploy/railway/bootstrap.sh ...`), and open the operator console from the side bar.

## Demo mode

`QUOTIFY_DEMO=true` on `payments-worker` and `admin-ui` (`bootstrap.sh --demo` sets both): every tenant, live ones included, is served by the mock
provider, and the console shows a banner. Never next to real provider accounts. Then `go run . demo --gateway https://<gateway domain> --operator-token "$TOKEN" --mailpit <mailpit url>`
plays the README story; Mailpit has no public domain, so give it one for the run (`railway domain --service mailpit --port 8025`) and delete it afterwards
(mail contents are readable by anyone with the URL). Switch back with `QUOTIFY_DEMO=false` on both.

## Mail

The notifier speaks plain SMTP without authentication (`SMTP_ADDR`), so out of the box mail goes to `mailpit`, which catches it and delivers nothing.
That is right for demo and sandbox; **real customers will not receive receipts**. For production, point `SMTP_ADDR` at a relay that accepts the
private network without a login (a Postfix or Haraka service in the project, or a provider's authenticated-relay-to-open-relay bridge), and set `MAIL_FROM`.
Adding SMTP authentication to the notifier is not done.

## Profiles and what is lost

| | full | small (`--profile small`) |
|---|---|---|
| Services | everything above | without `clickhouse` and `analytics-svc` |
| Lost | nothing | the console's analytics views and `/v1/analytics/*` answer *unavailable*; no ClickHouse (about 1 GiB) and one fewer process |
| Both | no Prometheus, Grafana, Tempo or OpenTelemetry collector: observability on Railway is its logs and per-service metrics. Leave `QUOTIFY_OTLP_ENDPOINT` empty (tracing off). `/metrics` is on each service's private port (9100) if you add your own scraper. |

The helm `values-small.yaml` also drops the mock provider; here `mockpsp` stays because sandbox tenants cannot pay without it. Set
`QUOTIFY_ALLOWED_ENVIRONMENTS=live` on `gateway` and `plans-svc` and drop `mockpsp` and `mailpit` for a live-only install.

## Cost and memory

Railway bills for resources used, not for limits, so the figures that matter are resident memory. The compose limits are the ceiling each service was
designed to stay under: gateway, plans, ingest, limits, analytics, invoice-worker 128 MiB; aggregator, billing 192 MiB; assets, payments-worker,
notifier, mockpsp 96 MiB; about 1.5 GiB for the 12 Go processes at the limit, usually well under that. The console (`vite preview` on Node) is about 100 MiB. Backing services
are heavier: Redpanda 512 MiB by design (`REDPANDA_MEMORY`), RabbitMQ up to 256 MiB of broker memory, ClickHouse up to 1 GiB, MinIO about 100 to 400 MiB,
Postgres and Redis whatever their plugins use. Plan for roughly 3 GiB for the small profile and 4 GiB for the full one, plus volumes, and check the
plan's per-service and per-project limits. These are estimates from the compose and helm limits, not Railway measurements. Nothing is Railway-sized
automatically; to cap a service use *Settings → Resource limits*.

## What is verified

Verified (static checks, `make railway-check`, run in CI as part of `make test`):

- every built compose service has a config file; each names a Dockerfile that exists, rooted watch patterns, a health path the service serves,
  a restart policy; only keys of Railway's published JSON schema are used;
- every `env:"...,required"` setting of every service is in its table above and set by `bootstrap.sh`; every `go_service` directory has its main package;
- `bootstrap.sh --dry-run` runs for both profiles and names every service.

Run locally with Docker (not on Railway):

- `go.Dockerfile` builds a service image; the console (`vite preview`) starts with an injected `PORT` and answers on IPv4 and IPv6;
- `init-postgres` creates the roles and databases, is idempotent, rotates passwords, and the roles cannot cross databases or create in `public`;
- the RabbitMQ image renders the password into the definitions, loads the queues, rejects the local password, and listens on `::` (AMQP and health);
- the Redpanda image starts with its admin health endpoint, and `init-redpanda` creates the seven topics.

**Not verified: everything that needs Railway itself.** In particular:

- no deploy has happened; the CLI commands in `bootstrap.sh` follow the CLI's `--help` (v5.55) but were never run against an account, and the shape of
  `railway service list --json` / `volume list --json` / `domain list --json` that the script matches for idempotency is assumed;
- that `service source connect` deploys a service whose variables are already set, and the order and waits of the staged deploy;
- that Railway passes service variables to `go.Dockerfile` as the `SERVICE_DIR` build argument, and reads config-as-code paths set in the dashboard;
- reference variables such as `${{Postgres.RAILWAY_PRIVATE_DOMAIN}}` and `${{minio.RAILWAY_PUBLIC_DOMAIN}}`, and that Postgres's `postgres` user can create `BYPASSRLS` roles;
- private-network DNS and routing (`*.railway.internal`, IPv6 or dual-stack) for gRPC, AMQP, Kafka and ClickHouse; Redpanda's `::` binding;
- volume permissions with `RAILWAY_RUN_UID=0` for Redpanda, RabbitMQ, ClickHouse and MinIO, and the MinIO and ClickHouse images on Railway;
- health checks against the real services, memory figures, and cost.
