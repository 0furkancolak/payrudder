# Phase 28 — Cells (schema per tenant)

See [ADR 0018](../adr/0018-tenant-isolation-modes.md). Large; delivered in stages, each releasable.

1. **Directory.** `tenants.cell`, `region`, `status`; `pkg/tenantdir` (process cache, stale-if-error); no behaviour change.
2. **Isolation switch.** `pkg/pgxx` picks RLS setting or `SET LOCAL search_path` by placement; a tenant-schema provisioner and a migration runner over all schemas (resumable, idempotent).
3. **Cross-tenant jobs** converted one at a time (outbox relay, billing tick, webhook dispatcher, fee run), each tested in both modes; the directory refuses a cell placement until all are converted.
4. **Moves.** Fenced, offline move of a tenant between cells; `doctor` command for placements.

## Exit criteria (per stage)

The existing integration suites pass in pooled mode unchanged and in cells mode for every converted module; a tenant in a cell cannot read another's schema (test); a move loses no row (test).
