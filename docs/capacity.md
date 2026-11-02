# Capacity

What the platform has been measured to do, where the first limit was seen, and how to find the cause of one before anything is added. This is the capacity side of [plan 17](plans/17-regions-scaling.md) and [plan 22](plans/22-platform-audit-and-roadmap.md) G17. The numbers are copied from [benchmarks.md](benchmarks.md), where each links to its artifact; nothing here is a projection, and a number that is a derivation says so.

**What is missing is the demand.** The owner has not given expected tenants, events per day or invoices per day for one and three years, nor the availability target, so there is no "needed" column below, only "measured". Until there is, this document cannot say whether the platform is large enough; it can only say where it stops being fast.

## Measured floors

Machine: Apple M4, **6 vCPU and 2.9 GiB for the whole Docker VM**, which ran Postgres, Redpanda, RabbitMQ, ClickHouse, Redis, MinIO, the observability stack and every service (one replica each) next to k6, with the compose memory caps (96-192 MiB per container). A server is above this floor, not below it.

| Path | Load | Result | Limit seen |
|---|---|---|---|
| Ingest, steady | 5,000 events/s in batches of 100, 10 tenants, 3 min | p99 32.2 ms, 0 errors, aggregator lag p99 0.02 s | none; the 15 min soak is the nightly's |
| Ingest, spike | 1k to 20k events/s over a minute, held a minute, 20 tenants | 0 5xx; **p99 2.35 s at the 20k peak** (median 10 ms, p90 0.79 s); k6 dropped 315 iterations for lack of VUs; aggregator lag max 0.44 s | **latency at 20k events/s** (below) |
| Per-tenant quota | one tenant over its quota | 429s above it, neighbours' p99 +2.8 % | none |
| Entitlement reads | 2,000 reads/s | p50 0.9 ms, p95 9.1 ms, **p99 23.4 ms** (target 10 ms) | p99 at 2k/s; cause not investigated |
| Month close | 10,000 customers x 5 meters | 289.8 s for 10,000 invoices (target 5 min; mock PSP with no latency) | narrow: 60 s of the 118 s at 500 customers is a fixed wait |
| 30-day hourly series | 3,000,000 events of one tenant | p95 22.0 ms | none |
| Idempotency table insert | batches of 500, partitioned by month | **198,150 rows/s**, batch p50 7.2 ms, p99 47.7 ms | none |
| Gateway upload, assets-svc | 20 x 1 MiB form; 1 GiB direct upload | 10 MiB RSS growth; 12.4 MiB peak | none |

Chaos (`make chaos N=20000`): A1-A4, A8, A9 pass with 10 container kills and 2 network faults; A5, A6 pass in the month-close test; A7 skipped. The 1M-event nightly has not run on a CI runner at the time of writing ([benchmarks.md](benchmarks.md#correctness-under-chaos)).

## The one finding: ingest at 20k events/s

At 20k events/s, one ingest-svc and one Postgres queued requests (p99 2.35 s) instead of failing. **Where the time goes was not profiled.** [Plan 22 G17](plans/22-platform-audit-and-roadmap.md) names the first suspects: WAL fsync, and a single hot tenant partition.

What can be said from numbers already published, as a derivation and not a measurement:

- 20k events/s in batches of 100 is 200 requests/s. Per event ingest-svc writes one idempotency row and one outbox row in one transaction.
- The idempotency table alone took 198k rows/s in batches of 500 on the same VM (`idempotency_retention.txt`), i.e. 20k rows/s is about a tenth of what that insert path did **in isolation**. WAL fsync of that table is therefore not obviously the limit on that VM. The two tables together, under the whole stack and k6 (which itself dropped iterations), were not measured.
- The VM has 6 vCPUs for roughly twenty processes plus the load generator. A CPU-bound ingest-svc, a CPU-bound k6 or the Kafka producer path would all show as queued requests. They are not separated by anything measured.

So the honest statement is: the cause of the p99 at 20k/s is **unknown**, the most likely wrong answer is "add Postgres capacity", and the next step is a profile, not a system.

### Steps that find the cause (none run yet)

Run on the largest machine available, one change at a time, with `docker run --rm alpine df -h /` checked first (a full VM disk once produced a false result: [benchmarks.md](benchmarks.md#findings)).

1. **Reproduce.** `make up`, then `make load S=ingest_spike`; keep the k6 summary. Do it three times; the first run of a fresh stack is not the answer.
2. **Is the load generator the limit?** Run k6 on a different machine (or with `K6_ENV` giving more VUs); look at k6's `dropped_iterations` and its own CPU. If dropped iterations vanish and p99 does not move, k6 was not the limit.
3. **Which resource is saturated while the p99 rises?** `docker stats` and the Grafana dashboards (`deploy/grafana`): CPU of ingest-svc, of Postgres, of Redpanda; Postgres connections (`pg_stat_activity`, a pool is 4 connections by default: `pgxx.DefaultMaxConns`, so one ingest-svc may simply be waiting for one: `pgxpool` wait time is in the otelpgx metrics).
4. **Postgres, only if 3 points there.** Enable `pg_stat_statements` on a scratch Postgres (`-c shared_preload_libraries=pg_stat_statements`), repeat step 1, and read the top statements by total time and by `wal_bytes`. Then compare `synchronous_commit=on` with `off` for the same run: if throughput does not change, WAL fsync was not it. If it does, `pg_stat_wal` and `pg_stat_io` say how much of the time is the flush.
5. **A hot tenant partition**, only if one statement dominates: `ingest_spike` uses 20 tenants, `noisy_neighbor` 10. Run `ingest_steady` with one tenant against ten at the same total rate; a difference is the hot partition.
6. **ingest-svc itself, only if 3 points there.** The services expose no profiler. Add one temporary `net/http/pprof` listener on the metrics port in a scratch build, take a 30 s CPU profile at the peak, and remove it. That is the only code change this procedure may need, and only for the run.
7. **Write the result into [benchmarks.md](benchmarks.md#findings) with the artifact**, including a negative one. Only then decide among: more ingest replicas (`hpa` exists, off), a larger Postgres, partitioning, or a change of code.

## The first limit of each component, from the code and the benchmarks

| Component | What bounds it | Source |
|---|---|---|
| Kafka consumers (aggregator, limits-svc, analytics-svc) | the partitions of the topic: `usage.events.v1` has 12, so no more than 12 replicas of a group do work | `deploy/compose/redpanda/topics.sh`; `tests/helm` checks the KEDA bounds against it |
| Postgres connections | sum of the pools of all replicas, plus one pair per cell; compose sets `max_connections=200` | [scaling.md](runbooks/scaling.md#the-connection-budget) |
| Month close | 10,000 customers in 289.8 s against a 5 min target | benchmarks above |
| Entitlement reads | p99 23.4 ms at 2k/s against 10 ms; median under a millisecond | benchmarks above |
| Cell jobs | a job visits every serving tenant of every cell once per run (one transaction each): a cell with thousands of tenants needs the jobs batched | [cells.md](runbooks/cells.md#operating-notes) |
| A move | a tenant is down for the length of its copy; there is no online move | [cells.md](runbooks/cells.md#moving-a-tenant) |

## What to do about demand, once it is known

| If the number that grows is... | Look at first | Not before |
|---|---|---|
| events per second | the profile above; then `hpa` on ingest-svc and gateway, and KEDA on the aggregator | more Kafka partitions (the topic has 12; each doubling needs a plan for ordering and for the consumer groups) |
| tenants | connection budget; cell sizing; jobs per cell | a pooler (scaling.md) until the budget says Postgres refuses connections |
| invoices at month close | the run's fixed wait and the saga; `month_close` at the new size | partitioning invoices and the ledger: the tables are append-only and indexed by tenant; measure a month close at 10x before touching them |
| reads of the console and reports | the query that is slow, with `EXPLAIN` | a read replica: nothing routes reads to one today, and the code would have to know which reads tolerate lag |

## Needed from the owner

For one and three years: tenants, events per day and the peak per second, invoices per month, customers per tenant; the availability target per path (ingest, hosted pages, console); the regions and why (law, latency, a customer) — the last one decides whether [scaling.md](runbooks/scaling.md#what-a-region-is-default-not-agreed) is the right shape at all.

## Run in this session

`make load` and `make bench-idempotency` need the compose stack or a compose Postgres on the machine, which here was in use by other work and holds 2.9 GiB; **none of the k6 scenarios or benches were run for this document**, and the numbers above are the published ones. The only new measurements are the correctness tests behind the pooler and across two clusters (scaling.md), which measure behaviour and not throughput.
