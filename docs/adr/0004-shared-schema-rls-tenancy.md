# ADR 0004 — Shared schema + Row Level Security for multi-tenancy

- Status: Accepted
- Date: 2026-10-17

## Context

Payrudder serves many tenants from the same services. A cross-tenant data leak in a billing system is the worst failure
mode. We want isolation that survives a missing `WHERE tenant_id = …` in application code, without the operational cost
of per-tenant databases.

## Decision

- **Shared schema**: every tenant-owned table has `tenant_id uuid NOT NULL`, leading its PK or primary index.
- **Postgres RLS**: `ENABLE` + `FORCE ROW LEVEL SECURITY`, with a policy comparing `tenant_id` to `NULLIF(current_setting('app.tenant_id', true), '')::uuid` (wrapped as `app_tenant_id()` in each database).
- Request handlers use role `<svc>_app` (RLS enforced) via `pgxx.InTenantTx`, which sets the setting **transaction-locally**.
- Cross-tenant background work uses role `<svc>_system` (`BYPASSRLS`) through a separate pool. Import boundaries are lint-enforced.
- CI checks that every table with `tenant_id` has RLS forced, and a generated test suite verifies isolation for every policy.

## Alternatives considered

| Option | Why not |
|---|---|
| Database per tenant | Strongest isolation, but N migrations, N pools, and hard cross-tenant consumers (one Kafka batch spans tenants). Too heavy |
| Schema per tenant | Same migration fan-out problem, and `search_path` tricks with pooling are error-prone |
| Application-only filtering | One forgotten predicate leaks data. No defence in depth |
| RLS with session-level `SET` | Leaks tenant context across pooled connections. Rejected in favour of `set_config(…, true)` |

## Consequences

- A missing tenant context returns **zero rows**, not another tenant's rows.
- Slight per-query overhead (policy predicate). `tenant_id`-leading indexes make it free in practice.
- Two pools per service and discipline about which code gets the system pool.
- Noisy neighbours share the same tables, so fairness is handled at admission and in partitioning, not storage ([07-correctness.md §7](../07-correctness.md#7-noisy-neighbor)).
- A later move of large tenants to dedicated databases stays possible because every row is already tenant-keyed.
