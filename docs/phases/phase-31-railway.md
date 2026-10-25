# Phase 31 — Railway

## Goal

Payrudder deploys to [Railway](https://railway.com) from this repository: a config-as-code file per service, images for the backing services Railway has no
plugin for, a bootstrap script on the Railway CLI, and a step-by-step guide ([deploy/railway/README.md](../../deploy/railway/README.md)). Nothing could be
deployed while writing it (no account), so everything is checked statically and the guide says what was not.

## Topology

One project, one environment, the private network between services (`<name>.railway.internal`). Public: `gateway` (the API), `admin-ui` (the console),
`minio` (presigned links open in browsers). Railway plugins: Postgres, Redis. Built from the repo: `redpanda`, `rabbitmq`, `minio`, `clickhouse`,
`mailpit`, the three one-shot jobs (`init-postgres`, `init-redpanda`, `init-minio`) and the 13 services of this repository (12 Go services, `mockpsp` included, and the console).

## Decisions

1. **`PORT` in `pkg/config`.** Railway health-checks and routes to `PORT`. It fills `PAYRUDDER_HTTP_ADDR`, else `MOCKPSP_HTTP_ADDR`, else `PAYRUDDER_METRICS_ADDR`,
   unless that variable is set; absent `PORT`, nothing changes. gRPC ports stay fixed (private network only). The alternative, a per-service flag, would
   touch twelve mains.
2. **Health checks.** The gRPC services and workers are checked on `/readyz` of the ops port (`PORT`). The gateway's public port gets a liveness-only
   `/healthz` (`withLiveness`) so `/metrics` never becomes public.
3. **Listeners.** Go `:port` is dual-stack: no change. nginx gets `listen [::]`. Third-party images are set to `::` (RabbitMQ, ClickHouse, Mailpit);
   Redpanda is a switch (`REDPANDA_BIND`) because its IPv6 binding is the one thing I could not confirm.
4. **`PAYRUDDER_ENV=railway`** is added to the accepted values rather than reusing `kind`.
5. **One Dockerfile for the Go services** (`go.Dockerfile`, `SERVICE_DIR` build argument) because Railway needs cache-mount ids no shared file can spell;
   the per-service Dockerfiles stay as they are for compose, kind and CI. The console has its own (`admin.Dockerfile`, repo-root context).
6. **Postgres:** one managed instance, a database and three roles per service as in compose, created by an idempotent `init-postgres` job with
   three shared role passwords (a role reaches only its own database). The alternative, a Postgres service per Payrudder service, costs more than it isolates.
7. **Migrations at start** (`PAYRUDDER_MIGRATE_ON_START=true`, one replica): Railway's pre-deploy command cannot run `PAYRUDDER_MIGRATE_ONLY` on a distroless image.
8. **Secrets** live only in Railway, generated once by the script and never overwritten; services reference them with `${{service.VAR}}`, so there are no
   shared variables to create by hand.
9. **Profiles.** `full`, and `small` without ClickHouse and `analytics-svc`. Observability (Prometheus, Grafana, Tempo) is not offered on Railway in either.
   `mockpsp` and `mailpit` stay in both: sandbox tenants cannot pay or mail without them.
10. **Mail:** the notifier has no SMTP authentication, so out of the box mail is caught, not delivered. Documented, not fixed.
11. **No `limitOverride`** (resource caps) in the config files: unverified numbers that could OOM a service on a platform I cannot test.

## Tasks

- [x] `pkg/config`: `PORT` fallback, `EnvRailway`, tests
- [x] gateway: liveness on the public listener, test
- [x] admin-ui: nginx listens on `${PORT}` and `[::]` (checked in the stock nginx image)
- [x] `deploy/railway/services/*.railway.json` for the 13 compose-built services; `infra/*.railway.json` for the 8 others
- [x] `go.Dockerfile`, `admin.Dockerfile`
- [x] Images: postgres-init, redpanda, redpanda-init, rabbitmq, clickhouse, minio, minio-init, mailpit
- [x] `bootstrap.sh` (idempotent, `--dry-run`, profiles, demo, GitHub or local source) and `make railway-check`
- [x] `deploy/railway/README.md`: steps, env table per service, migrations, first tenant, demo, mail, profiles, cost, what is verified
- [x] `tests/railway`: configs, Dockerfiles, health paths, required settings in the README and the script, dry runs (runs in `make test`, so in CI)
- [x] Links from README.md, docs/12-kubernetes.md, docs/README.md, the roadmap

## Exit criteria

`make railway-check` and `make test` pass; the images that can run without Railway were run locally; the README lists what only a real deploy can confirm.

## As built

See "What is verified" in the [guide](../../deploy/railway/README.md#what-is-verified). In short: statically verified and partly run under Docker; **never
deployed to Railway**. The first real deploy will likely teach something about the CLI output the script matches, the config-as-code path (set by hand), and volume permissions.
