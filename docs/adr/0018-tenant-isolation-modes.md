# ADR 0018: Two tenant isolation modes: pooled (RLS) and cells (schema per tenant)

Status: accepted; pooled mode is built and is the only mode in use. Cells mode is built in four stages (phase 28): the directory, the isolation switch, the job conversion and moves are in the code, and **the job conversion is not finished**, so the directory refuses to place any tenant in a cell ([what is left](#where-it-stands)).

## Context

Today every table carries `tenant_id` and Postgres row-level security, forced, scopes each transaction to one tenant (`app.tenant_id`). That is cheap and correct for thousands of small tenants in one database. The owner may sell Payrudder as a service and wants isolation in the style of
[pgcells](https://github.com/0furkancolak/pgcells): a tenant is a schema in exactly one **cell** (a Postgres cluster behind a transaction-mode pooler), a small **directory** says where, connections are a budget, every call names its target.

## Decision

1. **Pooled mode stays the default** and is not removed. It needs one database per service and no per-tenant migration work.
2. **Cells mode is added beside it**, selected per tenant by its placement row in the directory (`tenants.cell`, empty = pooled). In cells mode the tenant's tables live in schema `t_<tenant>` of the cell's database; a transaction runs with `SET LOCAL search_path = t_<tenant>, shared` (local, so it is safe behind a transaction pooler); migrations are applied to every tenant schema by a resumable, idempotent command; moving a tenant between cells is an offline, fenced operation.
3. **The directory is plans-svc's tenant table**, extended with `cell`, `region` and `placement_status`; services cache placement in process and never read it per request.
4. **No change of query code**: sqlc queries stay unqualified; the isolation is the search path (cells) or the RLS setting (pooled), chosen by one function in `pkg/pgxx`.
5. **Cross-tenant jobs** (the billing tick, the webhook dispatcher, the outbox relay) iterate placements and run per tenant, instead of scanning shared tables.

## Consequences

- Phase 28 is large: the cross-tenant jobs are the hard part, because today they read shared tables with a bypass role. They are converted one at a time, each with a test that runs in both modes.
- Until a job is converted, a tenant in a cell cannot use the feature that job serves, and the directory refuses to place a tenant in a cell if any such job is unconverted (a check, not a convention).
- pgcells itself is TypeScript; nothing of it is imported. Its ideas (directory, one writer per tenant, explicit targets, fenced moves) are reimplemented in Go in `pkg/pgxx` and `pkg/tenantdir`.

## As built

### The directory (stage 1)

`tenants.cell`, `region` and `placement_status` (migration `00064` of plans-svc; the column is not called `status` because that is the tenant's life cycle: inactive, active, suspended). `placement_status` is `ready` or `moving`, and only `ready` serves traffic. `DirectoryService` (operator only, no gateway route) answers `GetPlacement` and `ListPlacements`, and changes a placement only through the three steps of a move. `pkg/tenantdir` is the client side: a process cache with a TTL (30 s) and **stale-if-error** (a placement changes only when an operator moves a tenant, so the last answer is served up to 15 minutes when plans-svc cannot be asked, and an unknown tenant is never served stale).

### The switch (stage 2)

`pgxx.Router` is a `Beginner` that knows placements. `InTenantTx(ctx, router, tenant, fn)` locates the tenant and opens the transaction on the pooled pool or on the pool of the tenant's cell; in a cell it sets `app.tenant_id` **and** `search_path = "t_<id>", shared` with `set_config(…, true)` (transaction-local). Row-level security stays on in the tenant schema, on purpose: the schema says where the tenant's data is, the policy is the second guard, so a statement that names another tenant's schema reads none of its rows and writes none (tested). A tenant that is moving, in a cell the process has no pool for, or whose placement the directory cannot give (and no stale answer exists) is **refused, never guessed**: a write to the wrong place is not undone by a retry. When the database says the schema is not where the router looked (a stale answer after a move), the answer is dropped.

A service **opts in** with `PAYRUDDER_CELLS` (a JSON object of cells to the app, system and migrator DSNs of its database there); unset, it uses its pools directly, exactly as before.

A service becomes cell-capable by shipping a **tenant migration set** (`migrations/tenant`): goose SQL for the tables a tenant owns, with no name of a schema, and functions that bind their own schema (`SET search_path FROM CURRENT`), run per schema with its own `goose_db_version`. `pgxx.ProvisionTenant` builds one schema (idempotent, resumable), `pgxx.MigrateTenants` brings every schema up (a failing schema does not stop the others; a lock per schema lets replicas race). The two sets are written by hand and kept equal by a test per service (`TestTenantSchemaMatchesPublic` compares tables, columns, constraints, indexes, policies, grants and function signatures of `public` with a tenant schema via `pgxx.SchemaShape`). Replaying the public migrations per schema was rejected: they hard-code `public` in definer functions, and they mix tables nobody tenant-scopes with the ones everybody does.

### The jobs (stage 3)

`pkg/tenantdir.Jobs` is the inventory: 41 entries (every service's tenant schema and every job that scans across tenants), found by reading every `main.go` and worker package. `tenantdir.CellsReady()` names what is left, and plans-svc's `BeginMove` and `FinishMove` return `cells_not_ready` while it does. Converting a job means it runs per serving tenant placed in a cell, in the tenant's schema (`Router.EachReady`, `pgxx.InTenantSchemaTx`), and is tested in both modes.

Converted: the **outbox relay** (`pkg/outbox`: a tenant is visited on its `NOTIFY`, whose payload is the tenant id, or on a sweep; same claim, publish and delete in one transaction; a row of another tenant stops the batch; a moving tenant is skipped; depth and watermark cover the cells, and the watermark is unknown while a tenant moves, because a watermark that skipped it could let a consumer close a period over unpublished events) and **all of ingest-svc** and **all of assets-svc** (their request paths, monitors, partition maintenance, sandbox reset, sweeper, verifier and orphan reconciler).

### Moves (stage 4)

A move is fenced and offline (`pkg/cells.MoveTenant`, `payrudderctl cells move`): the directory marks the tenant `moving` (every service refuses it, jobs skip it); after a settle time (cached answers expire) each cell source is **closed in the database** (the service roles lose `USAGE` on the schema and in-flight transactions are waited for with an `ACCESS EXCLUSIVE` lock), so a writer with a stale answer fails instead of writing to a place being emptied; each service's data is copied in binary `COPY` into the destination in one transaction that commits only if every table has the same row count and checksum on both sides; the directory points at the destination; the source is set aside (a cell schema is renamed `m_<id>_<unix>`, kept until dropped) or the tenant's pooled rows are deleted. Any failure before the directory flips undoes everything. A tenant can move pooled to cell, cell to cell, and cell back to pooled; into the pooled database identity values are renumbered in order (no other table may point at them). The fence of a pooled source is the directory alone (shared tables cannot be closed), which is why the settle time exists.

`payrudderctl cells doctor` compares the directory with every cell database: missing, leftover and stuck-moving tenants, schemas behind the migrations, schemas set aside by moves.

### Where it stands

- Stages 1 and 2 are complete. Stage 4 is complete for the services that have a tenant schema (ingest-svc, assets-svc).
- Stage 3 is **not** complete: 30 of 41 inventory entries are not converted, among them the billing tick and its sweeps, the webhook dispatcher and the fee run. Each of plans-svc, billing-svc, aggregator and payments-worker first needs a tenant migration set (billing-svc and payments-worker have only tenant tables; plans-svc and the aggregator also have global tables that stay in `shared`, which the tenant set must leave out). Until then no tenant can be placed in a cell, by the check above, and no real tenant is.
- Not verified: behind a real transaction-mode pooler (pgbouncer) and across two real clusters; `LISTEN` does not work behind such a pooler, so the relay's sweep carries the load there.
