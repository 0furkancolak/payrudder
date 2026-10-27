# Phase 28 — Cells (schema per tenant)

See [ADR 0018](../adr/0018-tenant-isolation-modes.md) (what is built and how) and [the runbook](../runbooks/cells.md). Large; delivered in stages, each releasable and each leaving pooled mode as it was.

| Stage | State |
|---|---|
| 1. **Directory.** `tenants.cell`, `region`, `placement_status`; `pkg/tenantdir` (process cache, stale-if-error) | **done** (plans-svc migration `00064`, `DirectoryService`) |
| 2. **Isolation switch.** `pkg/pgxx` picks the RLS setting or `SET LOCAL search_path` by placement; a tenant-schema provisioner and a migration runner over all schemas (resumable, idempotent) | **done** (`pgxx.Router`, `ProvisionTenant`, `MigrateTenants`, `SchemaShape`) |
| 3. **Cross-tenant jobs** converted one at a time, each tested in both modes; the directory refuses a cell placement until all are converted | **not finished**: 15 of 41 inventory entries are converted. The refusal is in place (`BeginMove` and `FinishMove`), so no tenant can be placed in a cell |
| 4. **Moves.** Fenced, offline move of a tenant; `doctor` for placements | **done** for the services that have a tenant schema (`pkg/cells`, `payrudderctl cells`) |

## What stage 3 has left

`payrudderctl cells jobs` prints the list (`pkg/tenantdir.Jobs`, the source of truth). Converted: **ingest-svc** (tenant schema, event write path, outbox relay, outbox monitor, watermark, partition maintenance, sandbox reset) **assets-svc** (tenant schema, session sweeper, upload verifier, orphan reconciler) **payments-worker** (tenant schema, command worker, provider webhook server, transfers, sandbox reset) and the **aggregator** (tenant schema, usage consumer split per placement, maintenance, replay manager, sandbox purge), plus the generic relay in `pkg/outbox` that every service's outbox uses.

Not converted (21), by service. Each service first needs a **tenant migration set** (`migrations/tenant`, kept equal to the public one by a `TestTenantSchemaMatchesPublic`), then each job a per-tenant run in both modes:

- **billing-svc** (14; no table without `tenant_id`, so its tenant set is its whole schema): the outbox relay, the billing scheduler tick, the run gauges, the fee run, and the sweeps of credit-note refunds, payment-link expiry, promotion reversals, re-rating resume, the tenant-event flusher, the saga sweeper, credit expiry, the e-invoice sweep, and the worker reply consumer. Its request handlers already open `InTenantTx` on a pool and need the router; most of its jobs run on the system pool with the tenant named in each statement.
- **plans-svc** (7; global tables stay in `shared`: accounts, tenants, apps, users, memberships, sessions, tokens, invitations, audits; the tenant set holds only the tables with a `tenant_id`, and the global tables it points at by foreign key need their references dropped): the outbox relay, the webhook dispatcher, the affiliate commission approver, the trial reminder, the sandbox reset runner and retention sweep, the SaaS account sweep.

When the last entry is converted the directory accepts a cell, and the first real placement is an operator's decision, not a default.

## Not verified

Behind a real transaction-mode pooler and across two clusters; the Kafka suite of ingest-svc in cells mode (`ITEST_CELLS=1`) is run by hand, not by CI, and so is payments-worker's (`ITEST_CELLS=1` runs its whole suite, RabbitMQ worker and webhooks included, with every tenant in a cell).

## Exit criteria (per stage)

The existing integration suites pass in pooled mode unchanged and in cells mode for every converted module; a tenant in a cell cannot read another's schema (test); a move loses no row (test).
