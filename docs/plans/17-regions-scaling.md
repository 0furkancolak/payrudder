# 17 — Regions and scaling

## The request
List 2 (9): **region-based operation and scaling**, **database scaling** and the like.

## Where we are
Existing building blocks: **cells** (ADR 0018) — tenants' own rows can live in a cell database, `placement` and `tenantdir` route by tenant, `TestTenantSchemaMatchesPublic` keeps schemas aligned; per-service Postgres roles (migrator/app/system with RLS); a Helm chart and kind cluster; k6 load tests and a chaos suite exist; geo data. Nothing is region-aware at the request level.

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
