# Payrudder on Railway

Everything to run the stack on [Railway](https://railway.com): the project as code (`iac/.railway/railway.ts`), a config-as-code file per service
(build and health check), images for the backing services Railway has no database for, and a script that drives the Railway CLI
(`bootstrap.sh`). The design and the decisions are in phase 31 and [plan 23](../../docs/plans/23-staging-and-production.md); the runbook is
[staging-and-production](../../docs/runbooks/staging-and-production.md).

> **Status: not yet run end to end.** Staging was brought up with an earlier version on 2026-11-06; this layout (IaC, R2, Nafru, PgBouncer, no init
> jobs) is newer. What *was* checked, and what was not, is listed under [What is verified](#what-is-verified). Read that before you rely on it.

## Topology

One Railway project, `payrudder`, with two environments, `staging` and `production`. Every service talks to the others on the private network
(`<service>.railway.internal`); only the gateway is public. The console is a Cloudflare Worker (ADR 0021) and objects live in Cloudflare R2: neither
is a Railway service.

| Service | Built from | Public | Volume | Canvas group |
|---|---|---|---|---|
| `Postgres`, `Redis` | Railway databases | no | managed | Data |
| `pgbouncer` | `images/pgbouncer` | no | none | Data |
| `clickhouse` | `images/clickhouse` | no | `/var/lib/clickhouse` | Data, production only |
| `rabbitmq` | `images/rabbitmq` | no | `/var/lib/rabbitmq` | Messaging |
| `redpanda` | `images/redpanda` | no | `/var/lib/redpanda/data` | Messaging (Kafka) |
| `gateway` | `go.Dockerfile` | **yes** (`--api-domain`) | none | API |
| `plans-svc` `ingest-svc` `aggregator` `limits-svc` `billing-svc` `assets-svc` | `go.Dockerfile` | no | none | API |
| `analytics-svc` | `go.Dockerfile` | no | none | API, production only |
| `payments-worker` `invoice-worker` `notifier` | `go.Dockerfile` | no | none | Workers |
| `mockpsp` | `go.Dockerfile` | no | none | Sandbox |

Service names are the compose names, lower-case, because they become DNS names. Do not rename them: the variables below refer to them.

## How it differs from the compose stack

- **`PORT`.** Railway health-checks and routes public traffic to the port in `PORT`. `pkg/config` honours it without changing the YAML/env contract: `PORT`
  fills `PAYRUDDER_HTTP_ADDR` (gateway), `MOCKPSP_HTTP_ADDR` (mock-psp) or else `PAYRUDDER_METRICS_ADDR`, unless that variable is set itself. So for the gRPC
  services and the workers `PORT` is the ops port that serves `/healthz`, `/readyz`, `/metrics`; their gRPC port stays fixed (7001 to 7007) and is reached
  on the private network. The gateway also answers `GET /healthz` on its public port (liveness only; `/metrics` stays on its private port 9100).
  `bootstrap.sh` sets `PORT` explicitly on every service (the tables below) so the addresses other services use are fixed.
- **IPv6.** Railway's private network is IPv6 (newer environments are dual-stack). Go's `:port` listeners are dual-stack, so every Payrudder listener works
  as it is. RabbitMQ and ClickHouse are configured for `::`, Redpanda listens on `REDPANDA_BIND` (default `0.0.0.0`; set it to `::` if your environment
  is IPv6-only).
- **`PAYRUDDER_ENV=railway`**, a new accepted value (it only labels traces and keeps gRPC reflection off).
- **Dockerfiles.** Railway rejects BuildKit cache mounts without its own `id`, so the services build from `go.Dockerfile`, which is
  `deploy/docker/go.Dockerfile` minus the cache mounts and with the service directory as the build argument `SERVICE_DIR` (a service variable).
- **Migrations** run at start (`PAYRUDDER_MIGRATE_ON_START=true`, one replica of each service), not in a Job. See [Migrations](#migrations).
- **Postgres** is one managed instance with a database and three roles per service (the same layout as `deploy/compose/postgres/01-databases.sh`). There
  is no init job: each service creates its own database and roles when it starts (`pgxx.Provision`, with `PAYRUDDER_PG_ADMIN_DSN=${{Postgres.DATABASE_URL}}`).
  The role passwords are shared by service: three secrets, `PG_MIGRATOR_PASSWORD`, `PG_APP_PASSWORD`, `PG_SYSTEM_PASSWORD`, held as variables of the
  `Postgres` service and referenced as `${{Postgres.PG_APP_PASSWORD}}`. A role can only connect to its own service's database, so sharing a password
  across services does not cross the service boundary. This needs a Postgres user that may `CREATE ROLE ... BYPASSRLS` and `CREATE DATABASE`: Railway's
  `postgres` user is a superuser. On a managed Postgres that is not (Neon, Supabase, RDS without `rds_superuser`), create the 24 roles yourself.
- **PgBouncer.** Only the app role goes through `pgbouncer` (transaction pooling, `pgbouncer.railway.internal:6432`). The system role LISTENs and takes
  session advisory locks, the migrator takes goose's session lock, so they and the admin DSN reach Postgres directly ([scaling](../../docs/runbooks/scaling.md)).
- **Kafka topics.** Redpanda does not create them; there is no init job either: `plans-svc` creates them at start (`PAYRUDDER_KAFKA_CREATE_TOPICS=true`,
  `kafkax.Topics`, the same list as `deploy/compose/redpanda/topics.sh`).
- **Objects** are in Cloudflare R2, not MinIO; **mail** goes through Nafru, there is no mail catcher (no Mailpit).

## Prerequisites

- A Railway project `payrudder` on a plan that allows the memory below, with the environments `staging` and `production`, and Railway's GitHub app
  installed for the repository (staging builds `main` from it).
- The [CLI](https://docs.railway.com/cli) (`brew install railway`), Node with `npm` (the IaC), `openssl`, `bash`. `railway login`, then `railway link`
  to the project.
- Cloudflare R2: the buckets `payrudder-invoices-<environment>` and `payrudder-assets-<environment>`, and an R2 API token for them.
- Nafru Email, with the sender domain verified.

The runbook's steps 0 and 1 cover all of them; `deploy/railway/setup-secrets.sh <environment>` collects the keys.

## Quick path

```bash
(set -a; . ~/.config/payrudder/staging.env; set +a
 deploy/railway/bootstrap.sh --dry-run --environment staging --console-url https://console-staging.payrudder.com \
   --api-domain api-staging.payrudder.com --allowed-environments sandbox)     # read what it will do, then run it without --dry-run
```

It applies `railway.ts` to the environment (`railway config apply`: services, databases, volumes, canvas groups, each service's build and health
check), creates the gateway's custom domain the first time, sets the variables (generating the secrets once, never overwriting one that exists),
and deploys in order: PgBouncer and the brokers, `plans-svc`, the rest, `gateway`, pausing `--wait` seconds (default 45) between stages. It stops
before deleting a resource that `railway.ts` no longer has, unless `--allow-destroy` is given. `make railway-check` runs the static tests and a dry run.

## Two environments (staging and production)

Payrudder's own deployment ([plan 23](../../docs/plans/23-staging-and-production.md), runbook
[staging-and-production](../../docs/runbooks/staging-and-production.md)) is one Railway project, `payrudder`, with the environments `staging` and
`production`. Services, volumes, variables and generated secrets are never shared between them. Run the script once per environment with that
environment's keys (`deploy/railway/setup-secrets.sh <environment>` collects them, with where each one comes from, and prints the command above).

| Flag or variable | Effect |
|---|---|
| `--environment staging\|production` | required; the Railway environment (it must exist). `production` gets ClickHouse and `analytics-svc` and is not connected to GitHub; any other environment builds `main` from GitHub |
| `--console-url` | required; the console is the Cloudflare Worker: `PAYRUDDER_APP_URL` is this address, and `PAYRUDDER_TRUSTED_PROXY_SECRET` is generated on the gateway (put the same value on the Worker as `EDGE_AUTH_SECRET`) |
| `--api-domain` | the gateway's custom domain; created with `railway domain` the first time (add the DNS records it prints, DNS only), then kept in the IaC. `PAYRUDDER_PUBLIC_URL` uses it instead of the generated domain |
| `--allowed-environments sandbox` | a sandbox-only deployment: live accounts are refused (plans-svc, gateway) |
| `--allow-destroy` | lets the IaC apply delete resources that `railway.ts` no longer has |
| `NAFRU_BASE_URL`, `NAFRU_API_KEY` | both or neither; without them no mail goes out. With them, all of the platform's mail goes through Nafru, sandbox tenants' too (with a marked subject) |
| `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY` | required: Cloudflare R2 (`https://<account id>.r2.cloudflarestorage.com` and an R2 API token's keys). `S3_INVOICES_BUCKET`, `S3_ASSETS_BUCKET` default to `payrudder-invoices-<environment>`, `payrudder-assets-<environment>`; `S3_REGION` to `auto` |
| `GEO_API_KEY` | this environment's nafru-geo key, as `PAYRUDDER_GEO_API_KEY` on the gateway |
| `PAYRUDDER_GOOGLE_CLIENT_ID`, `PAYRUDDER_GOOGLE_CLIENT_SECRET` | set on the gateway as they are: "Continue with Google" |
| `--deploy-only` | builds the Payrudder services from this checkout with `railway up --ci`, in order, and nothing else; with `RAILWAY_TOKEN` (a project token of one environment) it needs no login |

Staging is connected to GitHub: Railway deploys every push to `main`. Production is not connected, and a published GitHub release deploys it:
`.github/workflows/deploy-production.yml` runs `bootstrap.sh --deploy-only` with the secret `RAILWAY_TOKEN_PRODUCTION`, then the console.

## By hand (what the script does)

1. **Resources.** `cd deploy/railway/iac && npm ci && railway config plan --environment <env>` shows what `railway.ts` would change;
   `railway config apply --environment <env>` applies it. The file declares the `Postgres` and `Redis` databases, every service with its source
   (GitHub for staging, none for production), the volumes (5 GB each, on `rabbitmq`, `redpanda` and in production `clickhouse`), the canvas groups
   (Data, Messaging, API, Workers, Sandbox), and each service's build (`dockerfilePath`, watch patterns) and deploy settings (health check, restart
   policy), read from its `deploy/railway/{services,infra}/<name>.railway.json`. It does not own variables.
2. **Domain.** `railway domain api.example.com --service gateway --port 8000`, then the DNS records it prints. Later applies keep it when
   `PAYRUDDER_API_DOMAIN` is set (IaC cannot create a domain, only keep one). Set `PAYRUDDER_PUBLIC_URL=https://api.example.com` on the gateway and
   `billing-svc` (hosted invoice links start with it).
3. **Variables.** Set the tables below. Secrets are generated once and only ever live in Railway:

   | Secret | Held by | Generate with |
   |---|---|---|
   | `PG_MIGRATOR_PASSWORD`, `PG_APP_PASSWORD`, `PG_SYSTEM_PASSWORD` | `Postgres` | `openssl rand -hex 24` (hex: they go into URLs) |
   | `RABBITMQ_PASSWORD` | `rabbitmq` | `openssl rand -hex 24` |
   | `CLICKHOUSE_PASSWORD` | `clickhouse` | `openssl rand -hex 24` |
   | `PAYRUDDER_OPERATOR_TOKEN` | `gateway` | `openssl rand -hex 32` (at least 32 characters; the API's machine credential) |
   | `PAYRUDDER_TRUSTED_PROXY_SECRET` | `gateway` | `openssl rand -hex 32` (the same value goes on the console Worker as `EDGE_AUTH_SECRET`) |
   | `PAYRUDDER_OTP_PEPPER` | `plans-svc` | `openssl rand -hex 32` (keys the hash of one-time sign-in and recovery codes; plans-svc refuses to start without it, and `PAYRUDDER_ENV=railway` is not a development environment) |
   | `PAYMENTS_INTERNAL_TOKEN` | `payments-worker` | `openssl rand -hex 32` |
   | `NOTIFIER_MAIL_EVENTS_SECRET` | `notifier` | `openssl rand -hex 32` (signs the notifier's mail-events listener; the gateway refers to it) |
   | `PAYRUDDER_SECRETS_KEY` | `payments-worker` | `openssl rand -base64 32` (exactly 32 bytes; seals tenants' provider credentials and webhook secrets) |

   Losing `PAYRUDDER_SECRETS_KEY` makes every sealed credential unreadable: back it up outside Railway.
4. **Deploy in order**, waiting for each stage to be healthy: `pgbouncer`, `rabbitmq`, `redpanda` (and `clickhouse` in production); `plans-svc` (it
   creates the Kafka topics); `ingest-svc`, `aggregator`, `limits-svc`, `assets-svc`, `mockpsp`, `analytics-svc`; `billing-svc`, `payments-worker`,
   `invoice-worker`, `notifier`; `gateway`. Staging redeploys from its source (`railway service redeploy --from-source`), production uploads the
   checkout (`railway up`). A service started too early restarts (`ON_FAILURE`, 10 tries) until its dependencies answer, so a wrong order is slow,
   not fatal.

## Memory

Railway bills by resident memory. Every Go service gets `GOMEMLIMIT=96MiB` (override with `GO_MEMLIMIT=128MiB deploy/railway/bootstrap.sh ...`), and
`pkg/memlimit` defaults `GOGC` to 50, so a service that holds a few MiB of live data stays near that instead of growing to twice it. Database pools
are 4 connections each (`pool_max_conns` in a DSN raises it) and idle ones close after a minute. Measured on the compose stack, idle services sit at 4 to 26 MiB and 12 to 60 MiB under the e2e load.

## Environment variables

`PORT`, `PAYRUDDER_ENV` and `PAYRUDDER_SERVICE_NAME` are set on every Payrudder service (`railway`, the service name). `SERVICE_DIR` is the build
argument of the Go services; the Dockerfile path comes from each service's `railway.json` through the IaC. Values written `${{x.Y}}` are Railway reference
variables; `<role>` below is `app`, `system` or `migrator`. The names here are the ones the services read; the defaults are in each
`cmd/*/config.default.yaml`.

**Error tracking** (optional): `ERROR_TRACKING_URL=https://<public key>@host/<project id> deploy/railway/bootstrap.sh ...` sets `PAYRUDDER_ERROR_TRACKING_URL` on every Go service
(panics and unexpected errors go to a GlitchTip or Sentry project; [docs/08-observability.md](../../docs/08-observability.md#error-tracking-off-by-default)); unset, nothing is sent. The DSN holds a key,
so it lives in Railway's variables only. Not exercised against a real server.

Shorthand: **PGDSN(`db`, role)** = `postgres://<db>_<role>:${{Postgres.PG_<ROLE>_PASSWORD}}@<host>/<db>_db`, where `<host>` is
`pgbouncer.railway.internal:6432` for the app role and `${{Postgres.RAILWAY_PRIVATE_DOMAIN}}:5432` for the system and migrator roles;
**ADMIN** = `${{Postgres.DATABASE_URL}}` (the `postgres` user, direct);
**KAFKA** = `redpanda.railway.internal:9092`, **AMQP** = `amqp://payrudder:${{rabbitmq.RABBITMQ_PASSWORD}}@rabbitmq.railway.internal:5672/`.

### gateway

| Variable | Value | Note |
|---|---|---|
| `PAYRUDDER_SERVICE_NAME` | `gateway` | |
| `PORT` | `8000` | the HTTP API; the public domain's port |
| `PAYRUDDER_OPERATOR_TOKEN` | generated | at least 32 characters; the machine credential of the operator API (scripts, `payrudderctl`); never typed into the console |
| `PAYRUDDER_OPERATOR_EMAILS` | e.g. `you@example.com` | comma separated; people who sign in with one of these and have verified it are platform operators (empty: nobody) |
| `PAYRUDDER_OPERATOR_SUPPORT_EMAILS` | empty | comma separated; read-only support operators: they see the operator console and change nothing; an address on both lists is a support operator. Set it with `OPERATOR_SUPPORT_EMAILS=... deploy/railway/bootstrap.sh ...` |
| `PAYRUDDER_OPERATOR_REQUIRE_2FA` | unset (on) | operators need two-factor sign-in: unset it is on here (`PAYRUDDER_ENV=railway`); an operator sets it up on the console's security page first ([how](../../docs/audit-log-and-two-factor.md#two-factor-sign-in)) |
| `PAYRUDDER_TRUSTED_PROXY_SECRET` | generated, 32+ characters | the same value as the Worker secret `EDGE_AUTH_SECRET`; makes the gateway count sign-in limits per visitor ([runbook](../../docs/runbooks/console-first-deploy.md)) |
| `PAYRUDDER_GEO_API_KEY` | from nafru-geo | the key the address forms' country/region/district lists are read with ([geo-data](../../docs/geo-data.md)); empty: those lists answer 503 and the fields are plain text. `GEO_API_KEY=… deploy/railway/bootstrap.sh ...` sets it |
| `PAYRUDDER_APP_URL` | the `--console-url` address | where the browser returns after Google; also set on `plans-svc` for the links in mails |
| `PAYRUDDER_GOOGLE_CLIENT_ID`, `PAYRUDDER_GOOGLE_CLIENT_SECRET` | from Google | optional: both set shows "Continue with Google" ([how](../../docs/13-admin-ui.md#sign-in-methods)); register `<console domain>/gw/auth/google/callback` as the redirect URI |
| `PAYRUDDER_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `PAYRUDDER_INGEST_ADDR` | `dns:///ingest-svc.railway.internal:7002` | |
| `PAYRUDDER_AGGREGATOR_ADDR` | `dns:///aggregator.railway.internal:7003` | |
| `PAYRUDDER_LIMITS_ADDR` | `dns:///limits-svc.railway.internal:7004` | |
| `PAYRUDDER_BILLING_ADDR` | `dns:///billing-svc.railway.internal:7005` | |
| `PAYRUDDER_ANALYTICS_ADDR` | `dns:///analytics-svc.railway.internal:7006` | production only |
| `PAYRUDDER_ASSETS_ADDR` | `dns:///assets-svc.railway.internal:7007` | |
| `PAYRUDDER_REDIS_URL` | `${{Redis.REDIS_URL}}` | |
| `PAYRUDDER_ALLOWED_ENVIRONMENTS` | `sandbox,live` | `sandbox` alone refuses live accounts |
| `PAYRUDDER_PAYMENTS_URL` | `http://payments-worker.railway.internal:8095` | |
| `PAYRUDDER_PAYMENTS_INTERNAL_TOKEN` | `${{payments-worker.PAYMENTS_INTERNAL_TOKEN}}` | |
| `PAYRUDDER_PUBLIC_URL` | `https://<--api-domain>`, else `https://${{gateway.RAILWAY_PUBLIC_DOMAIN}}` | |
| `PAYRUDDER_NOTIFIER_EVENTS_URL` | `http://notifier.railway.internal:9111` | the notifier's signed listener, for the console's mail page; set with the next row or not at all (the gateway refuses to start with one) |
| `PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET` | `${{notifier.NOTIFIER_MAIL_EVENTS_SECRET}}` | |
| `PAYRUDDER_MAIL_WEBHOOK_SECRET` | from Nafru, `whsec_...` | optional: the signing secret of the provider's webhook of delivery events, which points at `https://<gateway domain>/inbound/mail-events`; unset, the route answers 404 and bounces and complaints never reach the suppression list. Needs the two rows above. `MAIL_WEBHOOK_SECRET=whsec_... deploy/railway/bootstrap.sh ...` sets it ([runbook](../../docs/runbooks/mail-suppression.md#the-providers-webhook-translated-by-the-gateway)) |

### plans-svc

| Variable | Value | Note |
|---|---|---|
| `PAYRUDDER_SERVICE_NAME` | `plans-svc` | |
| `PORT` | `9100` | ops port (health) |
| `PAYRUDDER_PG_APP_DSN` | PGDSN(`plans`, app) | |
| `PAYRUDDER_PG_SYSTEM_DSN` | PGDSN(`plans`, system) | |
| `PAYRUDDER_PG_MIGRATOR_DSN` | PGDSN(`plans`, migrator) | used when `PAYRUDDER_MIGRATE_ON_START` is true |
| `PAYRUDDER_MIGRATE_ON_START` | `true` | |
| `PAYRUDDER_PG_ADMIN_DSN` | ADMIN | creates the service's database and roles at start when they are missing (`pgxx.Provision`); direct, never through PgBouncer |
| `PAYRUDDER_REDIS_URL` | `${{Redis.REDIS_URL}}` | |
| `PAYRUDDER_KAFKA_BROKERS` | KAFKA | |
| `PAYRUDDER_KAFKA_CREATE_TOPICS` | `true` | creates the platform's topics at start (`kafkax.Topics`, replication factor 1); Redpanda does not auto-create them |
| `PAYRUDDER_SECRETS_KEY` | `${{payments-worker.PAYRUDDER_SECRETS_KEY}}` | the same key as payments-worker's |
| `PAYRUDDER_SECRETS_KEY_ID`, `PAYRUDDER_SECRETS_KEY_PREVIOUS`, `PAYRUDDER_SECRETS_KEY_PREVIOUS_ID` | unset | while the key is being replaced, `${{payments-worker.NAME}}` for each: both services hold the same keys |
| `PAYRUDDER_ALLOWED_ENVIRONMENTS` | `sandbox,live` | |

`PAYRUDDER_WEBHOOK_ALLOW_PRIVATE` stays at its default `false`: tenants' webhook endpoints may not point at the private network.

### ingest-svc

| Variable | Value | Note |
|---|---|---|
| `PAYRUDDER_SERVICE_NAME` | `ingest-svc` | |
| `PORT` | `9100` | |
| `PAYRUDDER_PG_APP_DSN` | PGDSN(`ingest`, app) | |
| `PAYRUDDER_PG_SYSTEM_DSN` | PGDSN(`ingest`, system) | |
| `PAYRUDDER_PG_MIGRATOR_DSN` | PGDSN(`ingest`, migrator) | |
| `PAYRUDDER_MIGRATE_ON_START` | `true` | |
| `PAYRUDDER_PG_ADMIN_DSN` | ADMIN | creates the service's database and roles at start when they are missing (`pgxx.Provision`); direct, never through PgBouncer |
| `PAYRUDDER_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `PAYRUDDER_KAFKA_BROKERS` | KAFKA | |

### aggregator

| Variable | Value | Note |
|---|---|---|
| `PAYRUDDER_SERVICE_NAME` | `aggregator` | |
| `PORT` | `9100` | |
| `PAYRUDDER_PG_APP_DSN` | PGDSN(`aggregator`, app) | |
| `PAYRUDDER_PG_SYSTEM_DSN` | PGDSN(`aggregator`, system) | |
| `PAYRUDDER_PG_MIGRATOR_DSN` | PGDSN(`aggregator`, migrator) | |
| `PAYRUDDER_MIGRATE_ON_START` | `true` | |
| `PAYRUDDER_PG_ADMIN_DSN` | ADMIN | creates the service's database and roles at start when they are missing (`pgxx.Provision`); direct, never through PgBouncer |
| `PAYRUDDER_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `PAYRUDDER_KAFKA_BROKERS` | KAFKA | |

### limits-svc

| Variable | Value | Note |
|---|---|---|
| `PAYRUDDER_SERVICE_NAME` | `limits-svc` | |
| `PORT` | `9100` | |
| `PAYRUDDER_REDIS_URL` | `${{Redis.REDIS_URL}}` | |
| `PAYRUDDER_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `PAYRUDDER_AGGREGATOR_ADDR` | `dns:///aggregator.railway.internal:7003` | |
| `PAYRUDDER_KAFKA_BROKERS` | KAFKA | |

### billing-svc

| Variable | Value | Note |
|---|---|---|
| `PAYRUDDER_SERVICE_NAME` | `billing-svc` | |
| `PORT` | `9100` | |
| `PAYRUDDER_PG_APP_DSN` | PGDSN(`billing`, app) | |
| `PAYRUDDER_PG_SYSTEM_DSN` | PGDSN(`billing`, system) | |
| `PAYRUDDER_PG_MIGRATOR_DSN` | PGDSN(`billing`, migrator) | |
| `PAYRUDDER_MIGRATE_ON_START` | `true` | |
| `PAYRUDDER_PG_ADMIN_DSN` | ADMIN | creates the service's database and roles at start when they are missing (`pgxx.Provision`); direct, never through PgBouncer |
| `PAYRUDDER_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `PAYRUDDER_INGEST_ADDR` | `dns:///ingest-svc.railway.internal:7002` | |
| `PAYRUDDER_AGGREGATOR_ADDR` | `dns:///aggregator.railway.internal:7003` | |
| `PAYRUDDER_KAFKA_BROKERS` | KAFKA | |
| `PAYRUDDER_RABBITMQ_URL` | AMQP | |
| `PAYRUDDER_PUBLIC_URL` | `https://${{gateway.RAILWAY_PUBLIC_DOMAIN}}` | optional: the link in the mail that asks a customer to confirm a payment (3-D Secure); without it the mail has no link |
| `S3_ENDPOINT` | `https://<account>.r2.cloudflarestorage.com` | Cloudflare R2; presigned invoice links use it too |
| `S3_BUCKET` | `payrudder-invoices-<environment>` | |
| `S3_ACCESS_KEY` | from the R2 API token | |
| `S3_SECRET_KEY` | from the R2 API token | |
| `S3_REGION` | `auto` | |

### analytics-svc

Production only.

| Variable | Value | Note |
|---|---|---|
| `PAYRUDDER_SERVICE_NAME` | `analytics-svc` | |
| `PORT` | `9100` | |
| `CLICKHOUSE_ADDR` | `clickhouse.railway.internal:9000` | |
| `CLICKHOUSE_DB` | `payrudder` | |
| `CLICKHOUSE_USER` | `payrudder` | |
| `CLICKHOUSE_PASSWORD` | `${{clickhouse.CLICKHOUSE_PASSWORD}}` | |
| `PAYRUDDER_KAFKA_BROKERS` | KAFKA | |

### assets-svc

| Variable | Value | Note |
|---|---|---|
| `PAYRUDDER_SERVICE_NAME` | `assets-svc` | |
| `PORT` | `9100` | |
| `PAYRUDDER_PG_APP_DSN` | PGDSN(`assets`, app) | |
| `PAYRUDDER_PG_SYSTEM_DSN` | PGDSN(`assets`, system) | |
| `PAYRUDDER_PG_MIGRATOR_DSN` | PGDSN(`assets`, migrator) | |
| `PAYRUDDER_MIGRATE_ON_START` | `true` | |
| `PAYRUDDER_PG_ADMIN_DSN` | ADMIN | creates the service's database and roles at start when they are missing (`pgxx.Provision`); direct, never through PgBouncer |
| `S3_ENDPOINT` | `https://<account>.r2.cloudflarestorage.com` | Cloudflare R2; presigned upload and download URLs use it too |
| `S3_BUCKET` | `payrudder-assets-<environment>` | the bucket also has a public R2 domain, `assets.payrudder.com` (`assets-staging.payrudder.com`), and CORS for the console's uploads (runbook step 0) |
| `S3_ACCESS_KEY` | from the R2 API token | |
| `S3_SECRET_KEY` | from the R2 API token | |
| `S3_REGION` | `auto` | |

### payments-worker

| Variable | Value | Note |
|---|---|---|
| `PAYRUDDER_SERVICE_NAME` | `payments-worker` | |
| `PORT` | `9100` | its HTTP API (provider webhooks, operator endpoints) stays on `PAYMENTS_HTTP_ADDR` `:8095` |
| `PAYRUDDER_PG_SYSTEM_DSN` | PGDSN(`payments`, system) | |
| `PAYRUDDER_PG_MIGRATOR_DSN` | PGDSN(`payments`, migrator) | |
| `PAYRUDDER_MIGRATE_ON_START` | `true` | |
| `PAYRUDDER_PG_ADMIN_DSN` | ADMIN | creates the service's database and roles at start when they are missing (`pgxx.Provision`); direct, never through PgBouncer |
| `PAYRUDDER_RABBITMQ_URL` | AMQP | |
| `PAYRUDDER_REDIS_URL` | `${{Redis.REDIS_URL}}` | optional: the circuit breaker of a provider account is shared by the replicas; without it each replica keeps its own |
| `PAYRUDDER_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `PAYRUDDER_BILLING_ADDR` | `dns:///billing-svc.railway.internal:7005` | |
| `MOCKPSP_URL` | `http://mockpsp.railway.internal:8090` | sandbox tenants always use the mock provider |
| `PAYRUDDER_DEMO` | `false` | `true` is [demo mode](#demo-mode) |
| `PAYRUDDER_SECRETS_KEY` | generated | 32 bytes, base64 |
| `PAYRUDDER_SECRETS_KEY_ID`, `PAYRUDDER_SECRETS_KEY_PREVIOUS`, `PAYRUDDER_SECRETS_KEY_PREVIOUS_ID` | unset | only while the key is being replaced: the id of the key above, the key it replaces and that key's id ([runbook](../../docs/runbooks/secrets-key-rotation.md)); not exercised on Railway |
| `PAYMENTS_INTERNAL_TOKEN` | generated | shared with the gateway |
| `PAYMENTS_WEBHOOK_RECLAIM_AFTER` | unset (`10m`) | optional: how long a provider notification may stay `received` or `failed` in the inbox before the provider's retry takes it over and processes it again; at least `1m` |

Leave `PAYMENTS_LIVE_PROVIDER` unset: live tenants then use the provider account they configure (iyzico, Paddle), never the mock.

### invoice-worker

| Variable | Value | Note |
|---|---|---|
| `PAYRUDDER_SERVICE_NAME` | `invoice-worker` | |
| `PORT` | `9100` | |
| `PAYRUDDER_RABBITMQ_URL` | AMQP | |
| `S3_ENDPOINT` | `https://<account>.r2.cloudflarestorage.com` | Cloudflare R2 |
| `S3_BUCKET` | `payrudder-invoices-<environment>` | |
| `S3_ASSETS_BUCKET` | `payrudder-assets-<environment>` | |
| `S3_ACCESS_KEY` | from the R2 API token | |
| `S3_SECRET_KEY` | from the R2 API token | |
| `S3_REGION` | `auto` | |

### notifier

| Variable | Value | Note |
|---|---|---|
| `PAYRUDDER_SERVICE_NAME` | `notifier` | |
| `PORT` | `9100` | |
| `PAYRUDDER_PG_SYSTEM_DSN` | PGDSN(`notifier`, system) | |
| `PAYRUDDER_PG_MIGRATOR_DSN` | PGDSN(`notifier`, migrator) | |
| `PAYRUDDER_MIGRATE_ON_START` | `true` | |
| `PAYRUDDER_PG_ADMIN_DSN` | ADMIN | creates the service's database and roles at start when they are missing (`pgxx.Provision`); direct, never through PgBouncer |
| `PAYRUDDER_RABBITMQ_URL` | AMQP | |
| `PAYRUDDER_KAFKA_BROKERS` | KAFKA | |
| `PAYRUDDER_PLANS_ADDR` | `dns:///plans-svc.railway.internal:7001` | |
| `PAYRUDDER_REDIS_URL` | `${{Redis.REDIS_URL}}` | optional: the mail rate limit's buckets are shared by the replicas; without it each replica keeps its own |
| `NAFRU_BASE_URL`, `NAFRU_API_KEY` | from Nafru | both or neither (without them the notifier sends no mail): the platform's mail, sandbox tenants' included, is sent through Nafru. `SMTP_ADDR` is not set; see [Mail](#mail) |
| `NOTIFIER_EVENTS_ADDR` | `:9111` | the signed listener for mail events and the suppression list ([runbook](../../docs/runbooks/mail-suppression.md)); only the project's private network reaches it, the gateway calls it. Empty switches it off, and the gateway's two variables must then be unset too |
| `NOTIFIER_MAIL_EVENTS_SECRET` | generated | at least 16 characters; signs every request; the gateway holds the same value by reference |

### mockpsp

| Variable | Value | Note |
|---|---|---|
| `PAYRUDDER_SERVICE_NAME` | `mockpsp` | |
| `PORT` | `8090` | its API; `/healthz` is on it |
| `PAYRUDDER_PG_APP_DSN` | PGDSN(`mockpsp`, app) | |
| `PAYRUDDER_PG_MIGRATOR_DSN` | PGDSN(`mockpsp`, migrator) | |
| `PAYRUDDER_MIGRATE_ON_START` | `true` | |
| `PAYRUDDER_PG_ADMIN_DSN` | ADMIN | creates the service's database and roles at start when they are missing (`pgxx.Provision`); direct, never through PgBouncer |

### Backing services

| Service | Variable | Value |
|---|---|---|
| `Postgres` | `PG_MIGRATOR_PASSWORD`, `PG_APP_PASSWORD`, `PG_SYSTEM_PASSWORD` | generated; the services' DSNs refer to them |
| `pgbouncer` | `PGBOUNCER_AUTH_PASSWORD`, `PORT`, optional `PG_HOST` | `${{Postgres.PGPASSWORD}}` (it looks the other roles up as `postgres`), `6432`, `postgres.railway.internal` |
| `redpanda` | `PORT`, `RAILWAY_RUN_UID`, optional `REDPANDA_BIND`, `REDPANDA_MEMORY` | `9644` (health: the admin API), `0`, `0.0.0.0` or `::`, `512M` |
| `rabbitmq` | `RABBITMQ_PASSWORD`, `PORT`, `RAILWAY_RUN_UID` | generated, `15692` (health: Prometheus endpoint), `0` |
| `clickhouse` | `CLICKHOUSE_DB`, `CLICKHOUSE_USER`, `CLICKHOUSE_PASSWORD`, `CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT`, `PORT`, `RAILWAY_RUN_UID` | `payrudder`, `payrudder`, generated, `1`, `8123`, `0` (production only) |

`RAILWAY_RUN_UID=0` is set on the services with a volume: Railway volumes are root-owned.

**Another S3** (Tigris, AWS): R2 is reached as a plain S3 endpoint, so another provider is the same inputs (`S3_ENDPOINT`, `S3_ACCESS_KEY`,
`S3_SECRET_KEY`, `S3_REGION`, and the bucket names in `S3_INVOICES_BUCKET`, `S3_ASSETS_BUCKET`); create the buckets yourself. **Another Kafka or
RabbitMQ** (Upstash, CloudAMQP): change `PAYRUDDER_KAFKA_BROKERS` or `PAYRUDDER_RABBITMQ_URL` and drop the service; the topics (`kafkax.Topics`, the
same as `deploy/compose/redpanda/topics.sh`) must exist (`plans-svc` creates them with replication factor 1, or create them yourself) and, for
RabbitMQ, the exchanges and queues from `deploy/rabbitmq/definitions.json` (import the file in the provider's management UI).

## Migrations

Each service with a database applies its own migrations when it starts, as the schema owner (`PAYRUDDER_MIGRATE_ON_START=true` and the `*_MIGRATOR_DSN`).
That is safe with one replica, which is what these configs run. Order that matters:

1. Before migrating, the service creates its database and roles when they are missing (`PAYRUDDER_PG_ADMIN_DSN`, `pgxx.Provision`); there is no init job.
2. `plans-svc` creates the Kafka topics (`PAYRUDDER_KAFKA_CREATE_TOPICS`; Redpanda does not auto-create them).
3. `plans-svc` first, then the others: the rest call it, and need its topics. The order in `bootstrap.sh` is this one.

To take the migrator credential out of the running services afterwards, set `PAYRUDDER_MIGRATE_ON_START=false` and remove `PAYRUDDER_PG_MIGRATOR_DSN`
and `PAYRUDDER_PG_ADMIN_DSN` from each; set them again before an upgrade that carries migrations. The bootstrap sets them again on every run. With more
than one replica, run the migration as its own deploy first (one replica), then scale: Railway has no pre-deploy hook that can run a distroless
image's `PAYRUDDER_MIGRATE_ONLY` mode.

## First tenant

The operator token (for the API and `payrudderctl`; the console never asks for it) is `railway variable list --service gateway --kv | grep PAYRUDDER_OPERATOR_TOKEN`. From a checkout:

```bash
cd tools/payrudderctl
go run . seed --gateway https://<gateway domain> --operator-token "$TOKEN" --currency TRY --timezone Europe/Istanbul --grace 24h
```

It creates an account with a sandbox and a live tenant, an API key for each (printed once) and a sample catalogue. Without `payrudderctl`, the call is
`POST /admin/v1/accounts` with `Authorization: Bearer <operator token>` ([api/openapi/v1.yaml](../../api/openapi/v1.yaml)). Open the console at the
`--console-url` address, sign up (or sign in with an e-mailed code) with an address you listed in `PAYRUDDER_OPERATOR_EMAILS` (set it with `OPERATOR_EMAILS=you@example.com deploy/railway/bootstrap.sh ...`), and open the operator console from the side bar.

## Demo mode

`PAYRUDDER_DEMO=true` on `payments-worker` (`bootstrap.sh --demo` sets it): every tenant, live ones included, is served by the mock provider. Never
next to real provider accounts. The console's demo banner belongs to the Worker now, not to this script. `payrudderctl demo` reads the story's mail
from Mailpit, which this deployment does not have: play it on the compose stack. Switch back with `PAYRUDDER_DEMO=false`.

## Mail

The platform's mail goes through Nafru Email (`NAFRU_BASE_URL`, `NAFRU_API_KEY` on the notifier; without them it sends no mail: no signup verification, no invoices by mail). There is
no mail catcher: sandbox tenants' mail goes through Nafru too, with a marked subject. `SMTP_ADDR` (Mailpit in the compose stack) is not set. A tenant
that connected its own Nafru account still sends through that.

Bounces and complaints reach the suppression list through Nafru's webhook of delivery events: make it at `https://<gateway domain>/inbound/mail-events` and give the gateway the
`whsec_...` secret it shows (`MAIL_WEBHOOK_SECRET`, the gateway's `PAYRUDDER_MAIL_WEBHOOK_SECRET` above). Until then the route answers 404 and the gateway logs that it is off.

## Staging and production: what is lost

| | production | staging |
|---|---|---|
| Services | everything above | without `clickhouse` and `analytics-svc` |
| Lost | nothing | the console's analytics views and `/v1/analytics/*` answer *unavailable*; no ClickHouse (about 1 GiB) and one fewer process |
| Both | no Prometheus, Grafana, Tempo or OpenTelemetry collector: observability on Railway is its logs and per-service metrics. Leave `PAYRUDDER_OTLP_ENDPOINT` empty (tracing off). `/metrics` is on each service's private port (9100) if you add your own scraper. |

The helm `values-small.yaml` also drops the mock provider; here `mockpsp` stays because sandbox tenants cannot pay without it. Set
`PAYRUDDER_ALLOWED_ENVIRONMENTS=live` on `gateway` and `plans-svc` and drop `mockpsp` for a live-only install.

## Cost and memory

Railway bills for resources used, not for limits, so the figures that matter are resident memory. The compose limits are the ceiling each service was
designed to stay under: gateway, plans, ingest, limits, analytics, invoice-worker 128 MiB; aggregator, billing 192 MiB; assets, payments-worker,
notifier, mockpsp 96 MiB; about 1.5 GiB for the 12 Go processes at the limit, usually well under that. Backing services
are heavier: Redpanda 512 MiB by design (`REDPANDA_MEMORY`), RabbitMQ up to 256 MiB of broker memory, ClickHouse up to 1 GiB,
Postgres and Redis whatever Railway's databases use. Plan for roughly 3 GiB for staging and 4 GiB for production, plus volumes, and check the
plan's per-service and per-project limits. These are estimates from the compose and helm limits, not Railway measurements. Nothing is Railway-sized
automatically; to cap a service use *Settings → Resource limits*.

## What is verified

Verified (static checks, `make railway-check`, run in CI as part of `make test`):

- every built compose service has a config file; each names a Dockerfile that exists, rooted watch patterns, a health path the service serves,
  a restart policy; only keys of Railway's published JSON schema are used; `railway.ts` declares every service;
- every `env:"...,required"` setting of every service is in its table above and set by `bootstrap.sh`; every `go_service` directory has its main package;
- `bootstrap.sh --dry-run` runs for staging and production and names every service; `MAIL_WEBHOOK_SECRET` reaches the gateway's `PAYRUDDER_MAIL_WEBHOOK_SECRET` and no other service.

Run locally with Docker (not on Railway):

- `go.Dockerfile` builds a service image;
- `pgxx.Provision` creates a service's roles and database from nothing and is idempotent (integration test, run twice);
- the RabbitMQ image renders the password into the definitions, loads the queues, rejects the local password, and listens on `::` (AMQP and health);
- the Redpanda image starts with its admin health endpoint, and `kafkax.EnsureTopics` creates the topics against Redpanda, twice (integration test).

**Not verified: everything that needs Railway itself.** In particular:

- that the gateway reaches the notifier's mail-events listener on `notifier.railway.internal:9111` (the same private-network question as above);
- that Nafru's delivery webhook reaches `https://<gateway domain>/inbound/mail-events` and verifies against `MAIL_WEBHOOK_SECRET`;
- this layout has not been deployed: `railway.ts` and `railway config plan/apply` were never run against the project, and the CLI commands in
  `bootstrap.sh` and the shape of `railway domain list --json` it matches are assumed;
- the order and waits of the staged deploy (`railway service redeploy --from-source` on staging, `railway up --detach` on production);
- that Railway passes service variables to `go.Dockerfile` as the `SERVICE_DIR` build argument;
- reference variables such as `${{Postgres.RAILWAY_PRIVATE_DOMAIN}}`, `${{Postgres.DATABASE_URL}}` and `${{Postgres.PG_APP_PASSWORD}}`, and that
  Postgres's `postgres` user can create `BYPASSRLS` roles and databases;
- PgBouncer on Railway: its `auth_query` as `postgres`, and the app role's connections through it;
- R2 from the services: presigned links, the assets bucket's public domain and its CORS;
- private-network DNS and routing (`*.railway.internal`, IPv6 or dual-stack) for gRPC, AMQP, Kafka and ClickHouse; Redpanda's `::` binding;
- volume permissions with `RAILWAY_RUN_UID=0` for Redpanda, RabbitMQ and ClickHouse, and the ClickHouse image on Railway;
- health checks against the real services, memory figures, and cost.
