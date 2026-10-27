# Runbook: cells (schema per tenant)

[ADR 0018](../adr/0018-tenant-isolation-modes.md). Cells mode is **off**: no tenant is placed in a cell, and plans-svc refuses to place one until every job of the platform runs per tenant schema (`payrudderctl cells jobs` lists what is left). Everything below is how it works once it is allowed, and what to do when it misbehaves. All of it was exercised against Postgres in containers; none of it against a real transaction-mode pooler or two real clusters.

## Vocabulary

| Word | Meaning |
|---|---|
| pooled | The tenant's rows are in the service's normal database, in shared tables, under row-level security. Every tenant is pooled today |
| cell | A Postgres database per service on some cluster (`ingest_db` in cell `c1` is another database than the pooled `ingest_db`), holding only tenant schemas |
| tenant schema | `t_<32 hex of the tenant id>` in a cell: the tenant's tables of that service. `shared` is the second schema of every search path; nothing is in it yet |
| placement | `tenants.cell`, `region`, `placement_status` in plans-svc: where the tenant is, and `ready` or `moving` |
| set aside | `m_<id>_<unix>`: the schema a move left behind in its source cell, closed to the service roles |

## Turning a service's cells on

A service ignores cells unless `PAYRUDDER_CELLS` is set. It is a JSON object from cell name (lower case, digits, hyphens, at most 31) to the three DSNs of **that service's** database in the cell:

```
PAYRUDDER_CELLS={"c1":{"app":"postgres://ingest_app:…@c1-db/ingest_db","system":"postgres://ingest_system:…@c1-db/ingest_db","migrator":"postgres://ingest_migrator:…@c1-db/ingest_db"}}
```

The cell database is prepared like a service database (`deploy/compose/postgres/01-databases.sh`: the three roles, `REVOKE CREATE ON SCHEMA public FROM PUBLIC`), and nothing is migrated into its `public` schema. With `PAYRUDDER_MIGRATE_ON_START=true` the service creates the `shared` schema and applies its tenant migrations to every tenant schema of every cell (a failing tenant is logged and the service starts; with `PAYRUDDER_MIGRATE_ONLY=true` a failing tenant fails the job). `payrudderctl cells migrate` does the same from outside. A service in cells mode needs `PAYRUDDER_PLANS_ADDR` (assets-svc did not have it before; ingest-svc already did): the directory is plans-svc's.

Services converted so far: **ingest-svc**, **assets-svc**, **aggregator**, **payments-worker** (it needs `PAYRUDDER_PLANS_ADDR` too, and it uses only the `system` DSN of each cell; the `app` one must still be given and is not used). Every other service has no tenant schema yet; do not set `PAYRUDDER_CELLS` for it.

## The operator tool

`payrudderctl cells …` runs inside the network (a job, a bastion): it dials plans-svc's gRPC (no gateway route exists for the directory) and each database with its owner role. A config file says where everything is; `${NAME}` is replaced by the environment variable, so DSNs stay out of the file:

```json
{
  "plans_addr": "plans-svc:7001",
  "cells": {"c1": {"region": "eu"}, "c2": {"region": "us"}},
  "services": {
    "ingest": {"migrations": "services/ingest-svc/migrations/tenant", "roles": ["ingest_app", "ingest_system"],
               "pooled": "${INGEST_MIGRATOR_DSN}", "cells": {"c1": "${INGEST_C1_MIGRATOR_DSN}", "c2": "${INGEST_C2_MIGRATOR_DSN}"}},
    "assets": {"migrations": "services/assets-svc/migrations/tenant", "roles": ["assets_app", "assets_system"],
               "pooled": "${ASSETS_MIGRATOR_DSN}", "cells": {"c1": "${ASSETS_C1_MIGRATOR_DSN}", "c2": "${ASSETS_C2_MIGRATOR_DSN}"}},
    "aggregator": {"migrations": "services/aggregator/migrations/tenant", "roles": ["aggregator_app", "aggregator_system"],
                   "pooled": "${AGGREGATOR_MIGRATOR_DSN}", "cells": {"c1": "${AGGREGATOR_C1_MIGRATOR_DSN}", "c2": "${AGGREGATOR_C2_MIGRATOR_DSN}"}},
    "payments": {"migrations": "services/payments-worker/migrations/tenant", "roles": ["payments_app", "payments_system"],
                 "pooled": "${PAYMENTS_MIGRATOR_DSN}", "cells": {"c1": "${PAYMENTS_C1_MIGRATOR_DSN}", "c2": "${PAYMENTS_C2_MIGRATOR_DSN}"}}
  }
}
```

Every service in the file is moved together with the tenant, so list **every** service that has a tenant schema, and only those. The tenant migration directories come from a checkout of the version that is deployed.

| Command | What it does |
|---|---|
| `cells jobs` | the inventory: which jobs are converted, and whether the directory will place tenants |
| `cells doctor` | compares the directory with the databases (below); exit status 1 when there is an error |
| `cells migrate` | `shared` and the pending tenant migrations in every schema of every cell, for every service |
| `cells move --tenant ID --to c1\|pooled` | a fenced move of the tenant's data of every service |
| `cells abort --tenant ID --to c1\|pooled` | gives up a move that was left half done |
| `cells retired [--drop NAME --service S --cell C]` | lists, or drops, the schemas moves set aside |

## Moving a tenant

```
payrudderctl cells move --config cells.json --tenant 0190… --to c1 [--settle 60s] [--fence-wait 30s]
```

What happens, in order (`pkg/cells.MoveTenant`):

1. The directory marks the tenant `moving` (a move to a cell is refused here while any job is unconverted). Services refuse its requests with `Unavailable` ("retry is safe") and jobs skip it. **The tenant is down from here until the end of step 4.**
2. Wait `--settle` (default 60 s: twice the directory cache TTL) so every service has dropped its old answer. Then each cell source is closed to the service roles (`REVOKE USAGE ON SCHEMA`) and in-flight transactions in it are waited for, at most `--fence-wait`; a writer that still has the old answer now fails with a permission error instead of writing. A pooled source cannot be closed this way: the settle time is its only fence, and a stale writer inside that time writes rows the copy then misses or deletes. Do not shorten `--settle` for a tenant leaving the pooled database.
3. For every service: the destination schema is built if it is a cell, emptied, and filled by `COPY` from a consistent snapshot of the source; each table's row count and a checksum of its rows are compared and a difference aborts the copy (nothing committed). Identity ids are kept between cells and renumbered into the pooled database.
4. The directory points at the destination and the tenant is `ready` again.
5. Each source is retired: a cell schema is renamed `m_<id>_<unix>` (still closed; the rows are there if the move turns out wrong), the pooled rows are deleted.

A failure in steps 1 to 3 undoes everything (sources opened, destinations dropped or emptied, the directory unfenced) and the command says so. The one exception is a lost answer to the last directory call: if the directory then says it moved, the move goes on; if it cannot be asked, **nothing is undone** and the message says to run the doctor and then either `cells abort` or finish by hand. A failure in step 5 is reported as a problem and the exit status is 1; the tenant is already served from the destination.

Interrupted tool (killed, network): the tenant is `moving`. `cells doctor` shows it. If the destination was never confirmed, `cells abort --tenant … --to …` opens the source, discards the destination and unfences. If the directory already points at the destination (the doctor shows the tenant in the new cell and `ready`), only step 5 is missing: `cells doctor` lists the old schema or the pooled rows.

Moves are offline by design: a large tenant is down for the time of its copy. There is no online move.

## The doctor

`cells doctor` prints findings, worst first, and changes nothing:

| Level | Finding | What to do |
|---|---|---|
| error | the directory places a tenant in a cell the config does not know | fix the config or the placement |
| error | a cell cannot be reached, or lacks `shared` | the network or the DSN; `cells migrate` creates `shared` |
| error | placed here by the directory but `t_<id>` does not exist | the tenant cannot be served. If a move did not finish, `cells abort`; otherwise restore the schema (a set-aside `m_…` schema can be renamed back) |
| error | a schema with no migration history | it was not built by the tenant migrations; rebuild it |
| warn | the tenant is `moving` | if no move is running, `cells abort` |
| warn | `t_<id>` is here but the directory has the tenant elsewhere | a leftover of a move or of a placement that was undone; check the data is where the directory says, then drop it |
| warn | migrations are behind (or ahead of this checkout) | `cells migrate` (or deploy the matching version) |
| warn / info | a schema set aside by a move, older / younger than 14 days | `cells retired --drop NAME --service S --cell C` once the move is trusted |
| info | the directory refuses placements in cells | the unconverted jobs; `cells jobs` |

## Operating notes

- **Connections.** Each service replica holds an app pool and a system pool per cell, each of at most 4 connections by default (`pool_max_conns` in the DSN). A cell with many tenants needs a transaction-mode pooler in front of it for the app role; the router sets everything with `set_config(…, true)`, which is transaction-local. The prepared-statement mode of the driver must suit the pooler (`default_query_exec_mode` in the DSN). **Not tested.**
- **The relay's `LISTEN`.** It needs a session connection: point the system DSN of a cell straight at the database, or accept that the outbox sweep (every 2 s) carries the load.
- **Cost of the jobs.** A job visits every serving tenant of every cell on each run (one transaction per tenant). That is the design (cells hold few, large tenants); a cell with thousands of tenants would need the jobs batched per cell.
- **Region.** `region` is recorded for the operator and shown by the directory; nothing routes on it.
- **The aggregator's consumer and moves.** `consumer_offsets` stays in the pooled database whatever the placements, and the usage consumer writes each cell tenant in its own transaction before the offset moves, so a batch with a failing tenant is retried whole (the tenants that were durable are skipped by their inbox). A tenant that is `moving` stops the partitions that carry its events, because skipping its events would lose them; moves are offline, so keep them short. A tenant with an open replay keeps the job across the move (the job is not counted as failed while the tenant is not serving).
- **Adding a table.** A new migration of a converted service is written twice: `migrations/NNNNN_….sql` and `migrations/tenant/NNNNN_….sql`. `TestTenantSchemaMatchesPublic` fails until the second exists and matches. Roll it out with the service (`PAYRUDDER_MIGRATE_ON_START`) or `cells migrate`.
- **Deleting a tenant** in a cell is `DROP SCHEMA t_<id> CASCADE` in each service's cell database after its placement is gone; there is no tool for it.
- **payments-worker has no row-level security** (its tables never had), so in a cell the schema is the only guard: a statement that names another tenant's schema is allowed by the grants. No code path does that and a test checks that no schema holds a row of another tenant; do not give `payments_system` of a cell to anything that is not payments-worker.
- **payments-worker's index of charge keys** is `shared.charge_keys` in each cell database (key to tenant), filled by a trigger on `payment_attempts`. It is how a notification of a provider whose URL names no tenant (iyzico) finds its tenant without looking in every schema. Nothing to maintain: a move re-fills it through the copy, a sandbox reset deletes the tenant's keys, and an entry left behind by a move names the same tenant (the directory says where it is). If a provider notification is answered `ignored` ("no charge with this reference") for a charge that exists, look for the key in `shared.charge_keys` of the tenant's cell first.
- **A payments-worker tenant cannot be moved into the pooled database** while the schema has `manual_receipts` pointing at `payment_ledger.id`: `cells move --to pooled` stops at the copy with the identity-column message and changes nothing. Cell to cell and pooled to cell work.
