# Runbook: regions, connection pooling and scaling

[Plan 17](../plans/17-regions-scaling.md), [ADR 0018](../adr/0018-tenant-isolation-modes.md), [cells](cells.md), [capacity](../capacity.md). Everything here is **off by default** and nothing is switched on without a measurement that asks for it ([plan 22](../plans/22-platform-audit-and-roadmap.md) G17). What was tested, and what was not, is at the end of each section.

## What a region is (default, not agreed)

The owner has not said which regions, why, or how large. Until then:

- A **region** is a data-residency boundary **and** a latency target: a named place (`eu`, `us`, `tr`) where a tenant's data at rest lives and whose gateway the tenant's clients call.
- A tenant has **exactly one home region**: `tenants.region` in plans-svc. Today it is set by one thing only: `payrudderctl cells move --to <cell>` records the region of the cell (`region` in the cells config file). A tenant that was never moved to a cell has no region: it is **not pinned**, and every gateway serves it. There is no way to give a tenant a region when it is created yet.
- **Sign-in and the directory are global**, in a primary region: users, accounts, API keys, tenants and the directory are plans-svc's pooled database ([cells](cells.md#vocabulary): they stay pooled for ever). One plans-svc, one directory; the regional services reach it over the network (`PAYRUDDER_PLANS_ADDR`).
- **Usage ingest and hosted pages are served by the tenant's region.** The gateway of a region refuses a tenant that lives in another one (below). Invoice and payment links are built from `PAYRUDDER_PUBLIC_URL` of the gateway that creates them, so a region's gateway has its own public URL and the links it hands out point back at it.
- **Replication of Kafka, RabbitMQ, Redis, ClickHouse and MinIO across regions is out of scope.** Each region would run its own.

### The topology a multi-region deployment would use

```
primary region                              region eu                         region us
  console, operator                           gateway (PAYRUDDER_REGION=eu)     gateway (PAYRUDDER_REGION=us)
  plans-svc  + its pooled Postgres  <--gRPC-- ingest, aggregator, limits,       ingest, aggregator, limits,
  (directory, users, accounts,                billing, assets, payments-worker   billing, assets, payments-worker
   api_keys, tenants)                         Kafka, RabbitMQ, Redis,            Kafka, RabbitMQ, Redis,
                                              ClickHouse, MinIO (own)            ClickHouse, MinIO (own)
                                              Postgres cell "eu1" (every         Postgres cell "us1"
                                              service's tenant schemas)
```

A tenant placed in cell `eu1` has its rows in `eu1`'s databases and its events in eu's Kafka and ClickHouse, and its clients call eu's gateway. Moving it to `us1` is `payrudderctl cells move` (offline, checksummed), after which the operator also has to re-point its clients' DNS and move what the move does not carry (Kafka history, ClickHouse rows, MinIO objects: none of it is moved by any tool).

### Limits of this topology (read before promising residency)

1. **plans-svc runs in one place.** A tenant's customers, plans, subscriptions and webhooks (the 36 tables of plans-svc's tenant schema) can be at rest in the region's cell, but the process that reads and writes them, and the Redis that caches API keys, are in the primary region. If the law needs processing in the region too, plans-svc would have to run per region with its own directory. That is not designed.
2. **People are global.** `users`, `account_members`, sessions and sign-in e-mails are in the primary region whatever the tenants' regions.
3. **Every regional service calls plans-svc across regions** (placements, tenant rows, entitlements, webhooks). Placements are cached 30 s and served stale for up to 15 min when plans-svc cannot be asked ([ADR 0018](../adr/0018-tenant-isolation-modes.md)); the rest is a round trip per call. Not measured.
4. **No tool moves Kafka, ClickHouse or MinIO data between regions.** Only Postgres rows move.
5. **The gateway guard is not a firewall.** It refuses a request that arrives at the wrong region's gateway; it does not stop a service in region A from being configured with a cell DSN in region B. That is the cells config and the network.

## Setting a tenant's region at creation (plan 17)

`tenants.region` used to be set only when a tenant was moved into a cell. An app now says where it lives when it is created, and both of its tenants (sandbox and live) get it:

- **From the console or the product API**: `POST /app/v1/apps` takes an optional `region`. The gateway offers the regions it knows (`GET /app/v1/regions`: its own `PAYRUDDER_REGION` and the names in `PAYRUDDER_REGION_URLS`, default its own) and refuses any other (`400 gateway.region.unknown`); without the field the app is pinned to **the region of the gateway that took the request**. With the guard off (no `PAYRUDDER_REGION`) there are no regions, nothing is pinned here and a `region` in the body is refused. The console's *Create an app* dialog shows a **Region** field only when the deployment offers more than one.
- **From the operator API**: `POST /admin/v1/accounts/{id}/apps` takes `region` (any valid name; the operator is trusted to know the topology).
- **Default**: plans-svc `PAYRUDDER_DEFAULT_REGION` (empty by default) pins the tenants of an app whose request names none and that did not come through a gateway with a region, and the first app (`default`) that the operator's `createAccount` makes.
- A name must be lower-case letters, digits and hyphens, at most 31 characters (`plans.app.bad_region`); the table checks the same.
- The tenant API and the app answers show it: `region` on the tenant (empty is not pinned).

**Default, not agreed:** a person choosing a region other than the one they are signed in at gets an app whose requests this console's gateway will refuse with 421 (the guard reads `tenants.region`); they reach it at that region's address. The console says so in the field's hint and does not follow the hint itself. The alternative is to offer only the gateway's own region until the console can follow `X-Payrudder-Region-Url`. A region cannot be changed from the console afterwards; moving a tenant (`payrudderctl cells move`) is the operator's.

**Tested:** the gateway (`TestAnAppIsCreatedInARegionOfTheDeployment`: the list, the default, a chosen one, an unknown one, a deployment without regions), plans-svc against a real Postgres (`TestPlansSvc/AnAppIsPinnedToTheRegionChosenOrTheDefault`: chosen, default, refused name, both tenants, `tenants.region` equals what the API says, the operator's way in), the console's mock e2e (the field appears only with more than one region and its choice is sent). The default is tested through the self-serve path (no region in the request, plans-svc default `eu`); the first app of the operator's `createAccount` takes it through the same `InsertApp` and has no test of its own. **Not tested:** two real gateways with a real plans-svc, the console following the hint.

## The gateway guard (PAYRUDDER_REGION)

```
PAYRUDDER_REGION=us
PAYRUDDER_REGION_URLS=eu=https://eu.api.example.com,us=https://us.api.example.com
```

With `PAYRUDDER_REGION` set, a tenant request (an API key, or a person or operator acting as a tenant) for a tenant whose `tenants.region` is another region is answered **`421` `gateway.region.wrong`** with `X-Payrudder-Region: eu` and, when the region is in `PAYRUDDER_REGION_URLS`, `X-Payrudder-Region-Url: https://eu.api.example.com`. The gateway **never redirects**: a redirect would send the key and the body to another host. A client or SDK that wants to follow the hint does so itself.

- The check runs after the key is verified (an invalid key learns nothing about where tenants live) and **before the permission check and the rate limit**: a refused request is not charged to the tenant and reaches no backend (`TestRegionGuardRefusesAnotherRegionsTenant`).
- A tenant with no region is served by every gateway (default, not agreed; the alternative is to treat "no region" as "the primary region" and refuse it elsewhere).
- The region comes from plans-svc's `DirectoryService.GetPlacement` through `pkg/tenantdir` (30 s cache, stale-if-error, as for the other services). If it cannot be had and there is no earlier answer, the request is refused `503 identity.tenant.lookup_unavailable` rather than guessed.
- Session sign-in, `/auth/*`, operator routes and the public routes (provider webhooks, hosted pages, the inbound-mail hook) are **not** region-checked. A hosted page or portal link carries a token and no tenant; it works in the region whose gateway made the link.
- Unset (the default) the gateway behaves as before and never asks the directory (`TestRegionGuardIsOffByDefault`).

**Tested:** the cases above with fakes (`services/gateway/internal/httpapi/region_test.go`), the settings' validation (`TestRegionSettings`). **Not tested:** two real gateways with a real plans-svc and DNS; the console following the hint; the SDKs (none reads the headers).

## Residency, tested across two Postgres clusters

`pkg/cells` `TestTenantRowsStayInTheirRegionsCluster` runs two Postgres containers (cell `c1` = region `eu` on the first, `c2` = `us` on the second): a tenant seeded in the pooled database is moved to `c1`, and then to `c2`. After each step the tenant has a schema in the cluster the directory names and in no other, except the schema the move set aside on the cluster it left (closed to the service roles, dropped by `cells retired --drop`); the other cluster answers "no such schema" for its tables. The move, the abort and the doctor also run across the two clusters (`TestMoveAcrossClustersBehindAPooler`, `TestMoveGivenUpAcrossClustersBehindAPooler`, `TestDoctorAcrossClustersBehindAPooler`).

These need `ITEST_POOLER=1` (several containers each) and run in the nightly workflow. **Not tested:** clusters in different networks or regions (latency, bandwidth of the `COPY` stream, which flows through the process running `payrudderctl`: run it close to the destination); a failure of a cluster in the middle of a move other than the cases of `cells_integration_test.go`.

## Connection pooling (PgBouncer, transaction mode)

A cell with many tenants, or many replicas, needs fewer server connections than client pools hold. PgBouncer 1.25 in **transaction** mode was put in front of the databases of the integration suites, and the findings are:

### The rule: the app role goes through the pooler; the system and migrator roles do not

| Role | Through the pooler? | Why |
|---|---|---|
| `<svc>_app` (request handlers) | **yes** | Every transaction names its tenant with `set_config('app.tenant_id', …, true)` and, in a cell, `set_config('search_path', …, true)`: transaction-local, so they end with the transaction and the next client of the server connection sees none of it (`TestTransactionPooling/the tenant scope does not leak between clients`: 12 clients, four tenants in three places, one server connection per database). |
| `<svc>_system` (consumers, relays, jobs) | **no, straight to Postgres** | The outbox relay `LISTEN`s: behind the pooler a notification never reaches it, so only the sweep (every 2 s in cells mode, 50 ms poll otherwise) wakes it. aggregator's replay jobs and billing-svc's runs take a **session** advisory lock (`pg_try_advisory_lock`) to run one replica per job: behind the pooler two clients both get the lock. A session `SET` stays on the server connection and is seen by the next client. All three are shown in `TestTransactionPooling/session state is not kept`. |
| `<svc>_migrator`, and the owner DSNs in `payrudderctl cells`' config | **no, straight to Postgres** | A tenant migration (`pgxx.ProvisionTenant`, `MigrateTenants`, and so every `cells move` into a cell and `cells migrate`) connects with `search_path` as a start-up parameter and goose takes a session lock. PgBouncer refuses the parameter: `unsupported startup parameter: search_path`, and a move through it gives up and undoes itself (`TestTransactionPooling/the owner role is refused`). **Do not add `search_path` to `ignore_startup_parameters`**: the migration would run, in the wrong schema. |

The transaction-local flags that [plan 22 G3](../plans/22-platform-audit-and-roadmap.md) added (`payrudder.tenant_move`, `payrudder.sandbox_reset`, read by the triggers that refuse to delete a financial row) are `set_config(…, true)` in the transaction that deletes, so they work behind a transaction pooler and end with the transaction (`TestTransactionPooling/transaction-local flags work`: refused without the flag, allowed with it, refused again for the next client on the same server connection). The same test shows the mistake to avoid: `set_config(…, false)` stays on the server connection and lets the next client delete. Every flag of this kind must be `true`.

### Prepared statements

pgx (the driver) prepares every statement under a name derived from its text and keeps it on the connection. A pooler must therefore track prepared statements, or the driver must not use them:

| Pooler | Driver setting | Result |
|---|---|---|
| PgBouncer 1.21 or later, `max_prepared_statements` > 0 (200 in the shipped config) | none (the default) | works: the suites run this way |
| PgBouncer before 1.21, or `max_prepared_statements = 0` | none | **fails** at once with `prepared statement "stmtcache_…" already exists` (SQLSTATE 42P05) |
| the same | `default_query_exec_mode=exec` in the app DSN (also `cache_describe`, `describe_exec`, `simple_protocol`) | works for the statements of `TestPreparedStatementsBehindAPooler`; the whole service suites were **not** run in these modes |

`default_query_exec_mode` is read from the DSN by `pgxx.NewPool`, so it is a change of `PAYRUDDER_PG_APP_DSN` and nothing else.

### What was run

- `pkg/cells` (ITEST_POOLER=1): the tests named above, two Postgres containers and one PgBouncer container.
- `ITEST_CELLS=1 ITEST_POOLER=1` on the integration suites of **ingest-svc**, **payments-worker**, **plans-svc**, **aggregator** and **billing-svc** (every tenant in a cell, the app role behind PgBouncer, system and migrator direct). See [plan 17](../plans/17-regions-scaling.md#status-2026-11-02-branch-featregions-scaling) for the result of each run.
- `pkg/pgxx` integration tests behind the pooler.

`testkit.StartPostgres` does this when `ITEST_POOLER` is set; nothing changes in the suites.

### Not verified

A pooler in a cluster (the Helm charts have none; this is a decision for the day a measurement asks for it), PgBouncer's behaviour under load beyond the suites, TLS to the pooler, a pooler per cell, high availability of the pooler, and `LISTEN` through a session-mode pooler as an alternative. The compose profile below is for development.

### Compose

```
make up-pooler        # make up, with the app role of plans, ingest, aggregator, billing and assets on pgbouncer:6432 (host port 42433)
```

`deploy/compose/compose.yaml` has the `pgbouncer` service (profile `pooler`) and `deploy/compose/compose.pooler.yaml` moves the five `PAYRUDDER_PG_APP_DSN`s. The config is `deploy/compose/pgbouncer/pgbouncer.ini`; its comments say why it is shaped so. It was started against a scratch Postgres and a client logged in through it with its own role; the stack was **not** brought up on it.

### The connection budget

Every replica of a service holds, per database it uses, an app pool and a system pool of at most `pool_max_conns` connections (4 each by default, `pgxx.DefaultMaxConns`), and one more pair **per cell**. Postgres needs `max_connections` above the sum over all replicas plus the migration jobs (compose: 200). With one replica of everything the stack holds 48 connections (five services with both pools = 40, payments-worker and notifier 4 each; mock-psp adds 4); each replica added to a service with both pools adds 8 per database it talks to, per cell. Behind a pooler the app side is bounded by `default_pool_size` per role and database instead, whatever the replicas; the system side is not. Work it out for a deployment before the number of replicas goes up, not after Postgres refuses a connection.

## Autoscaling

`hpa` (CPU, existing) and `keda` (backlog, new) are per-service blocks in `deploy/helm/payrudder/values.yaml`, both **off**.

- **Request-driven services** (gateway, ingest-svc): `hpa: { enabled: true, max: N, cpu: 70 }`. Unchanged.
- **Consumers**: `deploy/helm/payrudder/values-keda.yaml` turns on a KEDA `ScaledObject` for aggregator and limits-svc (Kafka lag on `usage.events.v1`, 5000 records per replica) and for invoice-worker and payments-worker (RabbitMQ queue length of `invoice.render` and `payment.charge`, 50 messages per replica). KEDA must be installed first.

```
helm upgrade --install payrudder deploy/helm/payrudder -f deploy/helm/payrudder/values-small.yaml -f deploy/helm/payrudder/values-keda.yaml
```

Things to know:

1. **The numbers are guesses.** Thresholds, polling (15 s), cooldown (300 s) and the bounds were not measured on a cluster. Watch `consumer_lag_seconds` and the queue depths for a week of real traffic, then change them.
2. **A Kafka consumer cannot use more replicas than the topic has partitions** (`usage.events.v1`: 12). KEDA caps the scale there on its own; `tests/helm` fails if a `ScaledObject` allows more replicas than the topic has partitions.
3. **KEDA's Kafka lag is the log end minus the offset committed to Kafka** for the group. The consumers commit their marks to Kafka after handling a batch (`kafkax` `AutoCommitMarks`) while their authoritative offsets are in Postgres, so the two follow each other closely but are not the same number.
4. **UNVERIFIED:** `hostFromEnv: PAYRUDDER_RABBITMQ_URL` reads the AMQP URL from the pod's environment, which this chart sets with `envFrom` (a Secret). KEDA's RabbitMQ page says "an environment variable on the scale target" and does not say whether `envFrom` is read. If it is not, use `host` with a `TriggerAuthentication`.
5. `hpa` and `keda` on one service is refused when the chart is rendered (KEDA makes its own HPA). With either on, the Deployment no longer sets `replicas`.
6. Scaling a consumer past two replicas was never exercised: the chaos suite runs two. Whether aggregator, limits-svc and payments-worker behave with eight is a test to write before relying on it.

`make helm-lint` renders and validates `values-keda.yaml` with the other profiles (kubeconform has no schema for a `ScaledObject`, so that kind is skipped there); `tests/helm` (`keda_test.go`) checks the `ScaledObject`s against the services' own consumer groups, the topic's partitions and the broker's queue definitions. **Not run:** a cluster with KEDA.

## What not to build yet

[Plan 22 G17](../plans/22-platform-audit-and-roadmap.md): *profile ingest at 20k/s before any new technology.* Read replicas, partitioning of the ledger and invoices, more Kafka partitions, a second ClickHouse and more ingest replicas each answer a specific bottleneck; none is justified by a number yet. [capacity.md](../capacity.md) has the measurements there are, the first bottleneck that was seen, and the steps that find the cause.
