# ADR 0018: Two tenant isolation modes: pooled (RLS) and cells (schema per tenant)

Status: accepted; pooled mode is built, cells are built in stages (phase 28).

## Context

Today every table carries `tenant_id` and Postgres row-level security, forced, scopes each transaction to one tenant (`app.tenant_id`). That is cheap and correct for thousands of small tenants in one database. The owner may sell Payrudder as a service and wants isolation in the style of
[pgcells](https://github.com/0furkancolak/pgcells): a tenant is a schema in exactly one **cell** (a Postgres cluster behind a transaction-mode pooler), a small **directory** says where, connections are a budget, every call names its target.

## Decision

1. **Pooled mode stays the default** and is not removed. It needs one database per service and no per-tenant migration work.
2. **Cells mode is added beside it**, selected per tenant by its placement row in the directory (`tenants.cell`, empty = pooled). In cells mode the tenant's tables live in schema `t_<tenant>` of the cell's database; a transaction runs with `SET LOCAL search_path = t_<tenant>, shared` (local, so it is safe behind a transaction pooler); migrations are applied to every tenant schema by a resumable, idempotent command; moving a tenant between cells is an offline, fenced operation.
3. **The directory is plans-svc's tenant table**, extended with `cell`, `region` and `status`; services cache placement in process and never read it per request.
4. **No change of query code**: sqlc queries stay unqualified; the isolation is the search path (cells) or the RLS setting (pooled), chosen by one function in `pkg/pgxx`.
5. **Cross-tenant jobs** (the billing tick, the webhook dispatcher, the outbox relay) iterate placements and run per tenant, instead of scanning shared tables.

## Consequences

- Phase 28 is large: the cross-tenant jobs are the hard part, because today they read shared tables with a bypass role. They are converted one at a time, each with a test that runs in both modes.
- Until a job is converted, a tenant in a cell cannot use the feature that job serves, and the directory refuses to place a tenant in a cell if any such job is unconverted (a check, not a convention).
- pgcells itself is TypeScript; nothing of it is imported. Its ideas (directory, one writer per tenant, explicit targets, fenced moves) are reimplemented in Go in `pkg/pgxx` and `pkg/tenantdir`.
