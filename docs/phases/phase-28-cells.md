# Phase 28 — Cells (schema per tenant)

See [ADR 0018](../adr/0018-tenant-isolation-modes.md) (what is built and how) and [the runbook](../runbooks/cells.md). Large; delivered in stages, each releasable and each leaving pooled mode as it was.

| Stage | State |
|---|---|
| 1. **Directory.** `tenants.cell`, `region`, `placement_status`; `pkg/tenantdir` (process cache, stale-if-error) | **done** (plans-svc migration `00064`, `DirectoryService`) |
| 2. **Isolation switch.** `pkg/pgxx` picks the RLS setting or `SET LOCAL search_path` by placement; a tenant-schema provisioner and a migration runner over all schemas (resumable, idempotent) | **done** (`pgxx.Router`, `ProvisionTenant`, `MigrateTenants`, `SchemaShape`) |
| 3. **Cross-tenant jobs** converted one at a time, each tested in both modes; the directory refuses a cell placement until all are converted | **not finished**: 18 of 41 inventory entries are converted (ingest-svc, assets-svc, plans-svc). The refusal is in place (`BeginMove` and `FinishMove`), so no tenant can be placed in a cell |
| 4. **Moves.** Fenced, offline move of a tenant; `doctor` for placements | **done** for the services that have a tenant schema (`pkg/cells`, `payrudderctl cells`) |

## What stage 3 has left

`payrudderctl cells jobs` prints the list (`pkg/tenantdir.Jobs`, the source of truth). Converted: **ingest-svc** (tenant schema, event write path, outbox relay, outbox monitor, watermark, partition maintenance, sandbox reset), **assets-svc** (tenant schema, session sweeper, upload verifier, orphan reconciler) and **plans-svc** (tenant schema of the 36 tables with a `tenant_id` that are looked at one tenant at a time, every request path through the router, outbox relay, webhook dispatcher, affiliate commission approver, trial reminder, sandbox reset runner and retention sweep, SaaS account sweep; its global tables, the keys and the reset queue stay in the pooled database, [plans-svc.md](../03-services/plans-svc.md#in-a-cell-phase-28)), plus the generic relay in `pkg/outbox` that every service's outbox uses.

Not converted (23), by service. Each service first needs a **tenant migration set** (`migrations/tenant`, kept equal to the public one by a `TestTenantSchemaMatchesPublic`), then each job a per-tenant run in both modes:

- **billing-svc** (14; no table without `tenant_id`, so its tenant set is its whole schema): the outbox relay, the billing scheduler tick, the run gauges, the fee run, and the sweeps of credit-note refunds, payment-link expiry, promotion reversals, re-rating resume, the tenant-event flusher, the saga sweeper, credit expiry, the e-invoice sweep, and the worker reply consumer. Its request handlers already open `InTenantTx` on a pool and need the router; most of its jobs run on the system pool with the tenant named in each statement.
- **aggregator** (5; `consumer_offsets` is global and stays in `shared`): the usage consumer (one Kafka batch spans tenants and is written in one transaction: it has to be split per placement), maintenance, the replay manager, the sandbox purge.
- **payments-worker** (4; no row-level security on any table today: its tables are written by the system role only): the command worker, the provider webhook server (it finds the tenant of a provider event by looking the key up across all tenants), transfers and the sandbox reset.

When the last entry is converted the directory accepts a cell, and the first real placement is an operator's decision, not a default.

## Not verified

Behind a real transaction-mode pooler and across two clusters; the Kafka suite of ingest-svc and the whole suite of plans-svc in cells mode (`ITEST_CELLS=1`) are run by hand, not by CI.

## Exit criteria (per stage)

The existing integration suites pass in pooled mode unchanged and in cells mode for every converted module; a tenant in a cell cannot read another's schema (test); a move loses no row (test).
