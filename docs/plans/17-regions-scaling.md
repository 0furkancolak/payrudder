# 17 — Regions and scaling

## The request
List 2 (9): **region-based operation and scaling**, **database scaling** and the like.

## Where we are
Existing building blocks: **cells** (ADR 0018) — tenants' own rows can live in a cell database, `placement` and `tenantdir` route by tenant, `TestTenantSchemaMatchesPublic` keeps schemas aligned; per-service Postgres roles (migrator/app/system with RLS); a Helm chart and kind cluster; k6 load tests and a chaos suite exist; geo data. Nothing was region-aware at the request level until the gateway guard of the [status below](#status-2026-11-02-branch-featregions-scaling) (off by default).

## Gaps
- No definition of a **region** (data residency? latency? both?), no mapping tenant → region, no regional gateways or DNS, no cross-region story for the operator console, the platform tenant, the SaaS accounts and sign-in (users are global).
- Database scaling: single primary per service, no read replicas, no pooler in production (PgBouncer), no partitioning of the big tables (usage events live in ClickHouse; ledger and invoices in Postgres), no resharding tool, no connection budget model; cells exist but moving a tenant between cells/regions is not an operation.
- Kafka/Redpanda, RabbitMQ, Redis, ClickHouse, MinIO: no multi-region topology or replication.
- Autoscaling (HPA/KEDA by queue lag), capacity numbers per service, SLOs, load targets: the benchmark docs exist but no capacity plan.
- Compliance: data residency (EU/TR/KSA) requirements are not written down.

## Research first
1. **Requirements from the owner**: which regions, why (law, latency, customers), expected tenants/events/invoices per day in 1 and 3 years, availability targets.
2. Read ADR 0018 and the cells runbook (`docs/runbooks/cells.md`); list what already works for placement and what is "planned".
3. Take the benchmark results and find the first bottleneck per service at the target load (measure, do not guess).
4. Options study: (a) one region, scale vertically/replicas; (b) cells inside one region; (c) regional stacks with a global control plane; (d) fully isolated regional deployments. Write the trade-offs.

## Decisions needed
Everything in "requirements", the cloud/Kubernetes target, managed vs self-run databases, residency rules, the operator console's topology, budget.

## Work
1. Requirements and capacity model document; ADR choosing the topology.
2. Tenant ↔ region model (`tenants.region`, immutability, placement) and the control plane (who knows where a tenant lives; the gateway resolves by API key prefix or tenant).
3. Database scaling step by step: PgBouncer, read replicas for reporting/console reads, partitioning of append-only tables by time, per-cell sizing, a tested **move tenant between cells** operation with verification.
4. Messaging/cache/storage per region as the ADR dictates; autoscaling policies with load tests that prove them.
5. Residency checks (a test that data of a region's tenant is not written outside it), disaster-recovery per region (16).
6. Operator console and billing of the platform itself across regions.

## Done means
The ADR is accepted; a two-region (or two-cell) test environment passes the k6 suite at the target load, a tenant move completes with verification, failover is exercised, residency tests pass.

## Not verifiable here
Anything at real scale or across real regions (needs the cloud).

## Risks
The largest architectural change in the list: do not start implementation before the ADR is accepted.

## Review addendum (2026-11-01)
- **Stale:** `tenants.cell`, `region` and `placement_status` exist, `DirectoryService` routes, and `payrudderctl cells move|abort|doctor|retired` moves a tenant with a checksummed copy (`docs/runbooks/cells.md`). Per-cell regions exist in `pkg/cells/config.go`. Cells stay off until every job is converted; two real clusters and a real pooler were not tested.
- Replace Work 2 and the "move" part of Work 3 with: route requests by `tenants.region` in the gateway, add a residency test, and test the existing move through PgBouncer and across two Postgres containers (`ITEST_CELLS=1` suites are run by hand today; add them to nightly).
- Tension with plan 22 G17: measure before adding systems. PgBouncer, replicas and partitioning stay behind a measurement.

## Status (2026-11-02, branch `feat/regions-scaling`)

The Review addendum's three pieces are built; the architecture decisions are **defaults, not agreed**, and the ADR the plan asks for is not written (it waits for the owner's requirements: nothing below needs it, and nothing below is on by default).

### Defaults chosen (not agreed)

1. A **region** is a data-residency boundary and a latency target.
2. Each tenant has **one home region**: `tenants.region`. It is set today only when a tenant is moved into a cell (the cell's region); a tenant with no region is **not pinned** and every gateway serves it. (Alternative: no region means "the primary region", refused elsewhere.)
3. Sign-in, users, accounts, API keys and the directory are **global**, in a primary region (plans-svc and its pooled database).
4. Usage ingest and hosted pages are served by the tenant's region gateway; a gateway refuses a tenant of another region with `421 gateway.region.wrong` and a hint header, and **never redirects**.
5. **Cross-region replication of Kafka, RabbitMQ, Redis, ClickHouse and MinIO is out of scope.** The topology a multi-region deployment would use and its limits (plans-svc runs in one place; people are global; no tool moves non-Postgres data) are in [the scaling runbook](../runbooks/scaling.md#what-a-region-is-default-not-agreed).
6. PgBouncer in transaction mode serves the **app role only**; the system and owner roles go straight to Postgres. No pooler is in the Helm charts.
7. Autoscaling of consumers by lag is a KEDA values file, off, with unmeasured thresholds.

### Built

| Piece | Where | Test |
|---|---|---|
| Gateway region guard (`PAYRUDDER_REGION`, `PAYRUDDER_REGION_URLS`; off by default) | `services/gateway/internal/httpapi/region.go`, `cmd/gateway/main.go`; error key `gateway.region.wrong` in six locales | `region_test.go` (refuses another region's tenant before any backend or rate limit; unpinned tenants pass; after authentication; fails closed; off by default), `TestRegionSettings`; fails without the guard |
| Residency across two Postgres clusters | `pkg/cells/pooler_integration_test.go` | `TestTenantRowsStayInTheirRegionsCluster` |
| Moves, abort and doctor across two clusters behind PgBouncer | same | `TestMoveAcrossClustersBehindAPooler`, `TestMoveGivenUpAcrossClustersBehindAPooler`, `TestDoctorAcrossClustersBehindAPooler` |
| What a transaction pooler does and does not keep | same | `TestTransactionPooling` (tenant scope, transaction-local flags of #284, session state, the owner role), `TestPreparedStatementsBehindAPooler`; a session-level `search_path` fails the first |
| Test infrastructure | `pkg/testkit/pooler.go`: `StartNetwork`, `StartPostgresOn`, `StartPgBouncer`; `ITEST_POOLER=1` puts the app DSNs of every `testkit` database behind a PgBouncer | used by all of the above and by the service suites |
| Compose profile `pooler`, `make up-pooler` | `deploy/compose/compose.yaml`, `compose.pooler.yaml`, `pgbouncer/` | config validated (`docker compose config`); the ini was run against a scratch Postgres; the stack was not brought up on it |
| Nightly | `.github/workflows/nightly.yml` job `cells`: `ITEST_CELLS=1` suites of ingest-svc, payments-worker, plans-svc, aggregator and billing-svc, with and without `ITEST_POOLER=1`, plus `pkg` | not run on a runner |
| KEDA / HPA values | `values.yaml` `keda` blocks, `values-keda.yaml`, `templates/services.yaml`; `make helm-lint` skips the `ScaledObject` kind | `tests/helm/keda_test.go` (off by default; group, topic partitions and queues match the code; hpa and keda together refused); `make helm-lint` |
| Runbook and capacity | `docs/runbooks/scaling.md`, `docs/capacity.md` | n/a |

### What broke or had to be known (found by the tests)

- **The owner role cannot go through a transaction pooler.** `ProvisionTenant`/`MigrateTenants` pass `search_path` as a connect-time parameter; PgBouncer refuses it (`unsupported startup parameter: search_path`), so every `cells move` into a cell and `cells migrate` through a pooler fails, and the move undoes itself cleanly. Ignoring the parameter in PgBouncer would build the tables in the wrong schema: the compose config says not to. **Documented, not changed** (a guard in `migrateSchema` cannot work in transaction mode).
- **The system role cannot either.** The outbox relay's `LISTEN` never receives; aggregator's replay jobs and billing-svc's runs take `pg_try_advisory_lock` (session level), which two clients both win behind a pooler. **Documented.**
- **Prepared statements:** pgx's default mode fails at once (`prepared statement … already exists`) behind a pooler that does not track them (PgBouncer before 1.21 or `max_prepared_statements=0`); `default_query_exec_mode=exec` (and three other modes) works. With PgBouncer 1.25 and `max_prepared_statements=200` the default mode works. Documented.
- **#284's flags are fine:** `payrudder.tenant_move` and `payrudder.sandbox_reset` are `set_config(…, true)` in the deleting transaction; they do not outlive it behind the pooler. A session-level flag would leak: every flag must stay `true`.
- Nothing needed fixing in `pkg/pgxx` or `pkg/cells`.

### Service suites behind the pooler (`ITEST_CELLS=1 ITEST_POOLER=1`, app role through PgBouncer 1.25, this machine, run once)

| Suite | Result |
|---|---|
| `pkg/cells`, `pkg/pgxx` | ok (215 s, 147 s) |
| ingest-svc `./internal/itest` | ok (209 s) |
| aggregator `./internal/itest`, `./internal/replay`, `./internal/usage/query` | ok (271 s, 62 s, 604 s) |
| billing-svc `./internal/itest` | ok (459 s) |
| payments-worker `./internal/itest` | ok (801 s). A first run stopped at Go's default 10 minute test timeout with every test that had finished passing; the rerun with `-timeout 100m` passed (the nightly sets it) |
| plans-svc `./internal/itest` | first run: `TestPlansSvc/PriceChange` failed in three subtests ("price-change facts in the outbox: 0"). **Not the pooler**: it fails the same with `ITEST_CELLS=1` alone, on main. The test counted the outbox on the pooled system pool, where a tenant in a cell has no row (plan 15's test, #287, was never run in cells mode). Fixed in the test (`pricechange_integration_test.go`, no service code); rerun ok (244 s) |

The load average was 15-20 on a 10-core machine, so the times are slow. Each suite ran once. The same suites **without** the pooler (the other half of the nightly matrix) were not run in full here; only `PriceChange` was, in cells mode.

### Not verified (needs a cluster, a region or the owner)

- A pooler, KEDA or two regions in a real deployment; latency between regions; the `COPY` of a move across a network.
- The KEDA scaling itself: the thresholds are guesses; `hostFromEnv` with `envFrom` is UNVERIFIED (KEDA's page is silent).
- Two real gateways with DNS and a real plans-svc; the console and the SDKs following the region hint (none reads it).
- The nightly job on a runner.
- Throughput: **no k6 scenario or bench was run.** The 20k events/s profile (plan 22 G17) is not done; [capacity.md](../capacity.md) has the procedure and what the published numbers say.

### Still open

- The owner's requirements (regions and why, volumes in 1 and 3 years, availability) and with them the ADR choosing the topology; until then the topology in the runbook is a sketch.
- Whether "no region" should mean "the primary region" (a tenant is still unpinned unless its app was created with a region, by a gateway with `PAYRUDDER_REGION` or with `PAYRUDDER_DEFAULT_REGION` set), and the console following `X-Payrudder-Region-Url` so that an app in another region can be opened from the same console.
- Read replicas, partitioning of the ledger and invoices, a pooler in the Helm chart, and an online move: each stays behind a measurement ([capacity.md](../capacity.md#what-to-do-about-demand-once-it-is-known)).
- Disaster recovery per region (plan 16), and the operator console's and the platform tenant's billing across regions.

### Update (2026-11-02, branch `feat/alerts-account-audit`): a tenant's region is set when its app is created

`POST /app/v1/apps` takes an optional `region`; the gateway offers the regions it knows (`GET /app/v1/regions`, from `PAYRUDDER_REGION` and `PAYRUDDER_REGION_URLS`, default its own) and refuses others (`gateway.region.unknown`); the operator's `createApp` takes any valid name; plans-svc `PAYRUDDER_DEFAULT_REGION` (empty: not pinned) is the default for a request that names none; both tenants of the app get it (`tenants.region` existed since migration 00001, so there is no migration), the API shows it (`Tenant.region`), and the console's *Create an app* dialog shows a **Region** field when the deployment offers more than one (six languages). **Defaults (not agreed):** the app is pinned to the region of the gateway that took the request; a person may choose another region of the deployment, in which case this console's own gateway refuses the app's requests with 421 (they reach it at that region's address; the console does not follow the hint yet); a region is not changed from the console afterwards. How it works, what was tested and what was not: [scaling runbook](../runbooks/scaling.md#setting-a-tenants-region-at-creation-plan-17). Not verified: two real gateways, the console following the hint.
