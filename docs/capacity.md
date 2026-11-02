# Capacity

What the platform has been measured to do, where the first limit was seen, and how to find the cause of one before anything is added. This is the capacity side of [plan 17](plans/17-regions-scaling.md) and [plan 22](plans/22-platform-audit-and-roadmap.md) G17. The numbers are copied from [benchmarks.md](benchmarks.md), where each links to its artifact; nothing here is a projection, and a number that is a derivation says so.

**What is missing is the demand.** The owner has not given expected tenants, events per day or invoices per day for one and three years, nor the availability target, so there is no "needed" column below, only "measured". Until there is, this document cannot say whether the platform is large enough; it can only say where it stops being fast.

## Measured floors

Machine: Apple M4, **6 vCPU and 2.9 GiB for the whole Docker VM**, which ran Postgres, Redpanda, RabbitMQ, ClickHouse, Redis, MinIO, the observability stack and every service (one replica each) next to k6, with the compose memory caps (96-192 MiB per container). A server is above this floor, not below it.

| Path | Load | Result | Limit seen |
|---|---|---|---|
| Ingest, steady | 5,000 events/s in batches of 100, 10 tenants, 3 min | p99 32.2 ms, 0 errors, aggregator lag p99 0.02 s | none; the 15 min soak is the nightly's |
| Ingest, spike | 1k to 20k events/s over a minute, held a minute, 20 tenants | 0 5xx; **p99 2.35 s at the 20k peak** (median 10 ms, p90 0.79 s); k6 dropped 315 iterations for lack of VUs; aggregator lag max 0.44 s | **latency at 20k events/s** (below) |
| Ingest, spike, **re-run 2026-11-02 on a contended machine** | the same scenario, 20 tenants, 1k to 20k events/s | **89.5 % of 20,062 requests answered 5xx** (`i/o timeout` writing to Postgres after k6's 5 s); 10k events/s steady for 90 s: 96.8 % | **the Docker VM's disk: WAL fsync took 75 to 162 ms** (a local SSD takes under 1 ms); [below](#profile-of-the-ingest-limit-2026-11-02) |
| Per-tenant quota | one tenant over its quota | 429s above it, neighbours' p99 +2.8 % | none |
| Entitlement reads | 2,000 reads/s | p50 0.9 ms, p95 9.1 ms, **p99 23.4 ms** (target 10 ms) | p99 at 2k/s; cause not investigated |
| Month close | 10,000 customers x 5 meters | 289.8 s for 10,000 invoices (target 5 min; mock PSP with no latency) | narrow: 60 s of the 118 s at 500 customers is a fixed wait |
| 30-day hourly series | 3,000,000 events of one tenant | p95 22.0 ms | none |
| Idempotency table insert | batches of 500, partitioned by month | **198,150 rows/s**, batch p50 7.2 ms, p99 47.7 ms | none |
| Gateway upload, assets-svc | 20 x 1 MiB form; 1 GiB direct upload | 10 MiB RSS growth; 12.4 MiB peak | none |

Chaos (`make chaos N=20000`): A1-A4, A8, A9 pass with 10 container kills and 2 network faults; A5, A6 pass in the month-close test; A7 skipped. The 1M-event nightly has run on a CI runner (green on 2026-10-30 to 11-02 on GitHub); its numbers are not copied into [benchmarks.md](benchmarks.md#correctness-under-chaos).

## The one finding: ingest at 20k events/s

At 20k events/s, one ingest-svc and one Postgres queued requests (p99 2.35 s) instead of failing. **Where the time goes was not profiled.** [Plan 22 G17](plans/22-platform-audit-and-roadmap.md) names the first suspects: WAL fsync, and a single hot tenant partition.

What can be said from numbers already published, as a derivation and not a measurement:

- 20k events/s in batches of 100 is 200 requests/s. Per event ingest-svc writes one idempotency row and one outbox row in one transaction.
- The idempotency table alone took 198k rows/s in batches of 500 on the same VM (`idempotency_retention.txt`), i.e. 20k rows/s is about a tenth of what that insert path did **in isolation**. WAL fsync of that table is therefore not obviously the limit on that VM. The two tables together, under the whole stack and k6 (which itself dropped iterations), were not measured.
- The VM has 6 vCPUs for roughly twenty processes plus the load generator. A CPU-bound ingest-svc, a CPU-bound k6 or the Kafka producer path would all show as queued requests. They are not separated by anything measured.

So the honest statement is: the cause of the p99 at 20k/s is **unknown**, the most likely wrong answer is "add Postgres capacity", and the next step is a profile, not a system.

### Profile of the ingest limit, 2026-11-02

Run for [plan 22 G17](plans/22-platform-audit-and-roadmap.md); artifacts: [`benchmarks/ingest_profile_2026-11-02.txt`](benchmarks/ingest_profile_2026-11-02.txt) (every number below, per run) and [`benchmarks/ingest_spike_20k_contended_2026-11-02.json`](benchmarks/ingest_spike_20k_contended_2026-11-02.json). **No code was changed.** The machine was shared and busy: the host's load average was 6 to 15 on 10 cores during every run (other people's builds and tests), the Docker VM has 2.9 GiB and 6 vCPU for about twenty containers of several projects, and k6 ran in the same VM. This is **not** the 20k/s profile on a quiet machine, which was not possible here; it is what the profile shows on the machine that was available, and the machine turned out to be the limit.

How it was run (to repeat it): a reduced stack under its own compose project (`-p payrudder-k6`, every host port reset so that it does not touch a stack that is already up): Postgres, Redpanda, Redis, RabbitMQ, MinIO, Mailpit, plans-svc, ingest-svc, aggregator, limits-svc, billing-svc, assets-svc, gateway (no ClickHouse, analytics-svc, payments-worker or observability), `PAYRUDDER_SAAS=off`, one replica each. **The service images were not built in the VM**: a first attempt to build seven Go images at once there crashed Docker's engine and took the other projects' containers down with it (restarted by hand afterwards). Build the binaries on the host (`GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"`) and put each on `gcr.io/distroless/static-debian12:nonroot`. k6 2.3.0 from `grafana/k6` on the project's network against `http://gateway:8000`, with `track_wal_io_timing` on in the scratch Postgres, `pg_stat_activity` sampled every second and `docker stats` every few seconds.

| Run | What changed | Requests | Failed | Median / p99 | WAL sync per call |
|---|---|---|---|---|---|
| `ingest_spike`, 20 tenants, peak 20k events/s | nothing | 20,062 | **89.5 %** | 5.0 s / 5.5 s | not measured |
| A: `ingest_steady` 10,000 events/s, 90 s | nothing | 8,787 | **96.8 %** | 5.0 s / 5.3 s | 89 ms |
| B | `pg_notify` removed from `enqueue_outbox` (scratch database) | 8,835 | 95.8 % | 5.0 s / 5.0 s | 75 ms |
| C | B and `synchronous_commit = off` | 8,751 | 36.1 % | 4.8 s / 5.1 s | 125 ms |
| D | ingest-svc pools of 64 and 16 instead of 4 | 8,720 | 96.7 % | 5.0 s / 5.4 s | 132 ms |
| E | B and D | 8,755 | 73.2 % | 5.0 s / 5.3 s | 162 ms |

(The k6 clock is 5 s per request, so the median of a failing run is the timeout. A 500 events/s smoke run on the same stack passed: 341 requests, none failed, p99 313 ms.)

**What the profile shows**

1. **The disk under Postgres is the first limit on this machine.** `pg_test_fsync` in the Postgres container, idle, measured 25 to 126 ms per sync (a local SSD: well under a millisecond), and `pg_stat_wal` over each run gives 75 to 162 ms per WAL sync. The commit of every ingest batch waits for one. With `synchronous_commit = off` (C) the failures fell from 96 % to 36 %; nothing else changed that much. The earlier published run (p99 2.35 s, no 5xx, on a quiet machine) and the one discarded run with the same `i/o timeout` (a full disk) are consistent with this: the symptom is Postgres not keeping up with commits, and `i/o timeout` is ingest-svc giving up on a write.
2. **Where the ingest_db backends waited** (sampled active backends): in A, `LWLock:WALWrite` 35 % and `IO:WalSync` 15 % (the flush), CPU 33 %, **`Lock:object` 14 %**; in the spike `Lock:object` was 32.5 % of ingest_db's backend time, with the flush at 35 %; with a pool of 64 (D) `Lock:object` was **38 %**. `Lock:object` is the lock PostgreSQL takes at commit of a transaction that sent a `NOTIFY` so that notifications are queued in commit order; `enqueue_outbox` ends with `pg_notify('outbox_new', '')`, so the commits of the ingest transactions of a database are serialised through it, flush included. With few connections it hides behind the flush; the more writers wait, the more of them wait on it.
3. **Neither the notify nor the pool is the cause on its own here.** Removing `pg_notify` alone (B) and a bigger pool alone (D) left 96 % failing; both together (E) 73 %. The flush time was 75 to 162 ms in every run, which the services cannot change.
4. **The services were not CPU-bound.** In A, ingest-svc used about 49 % of a core (median), the gateway 17 %, Postgres about 157 % (of the VM's 600 %); in the spike their peaks were 182 %, 178 % and 186 %, every other container under 70 %. Memory: at most 1,410 MiB of the VM's 2,980.
5. **WAL volume.** About 355 to 905 MB of WAL for 8 to 9 thousand requests of 100 events (`wal_level = logical`, an idempotency row and an outbox row per event and their indexes): the per-event WAL cost is not small, and it multiplies the effect of a slow flush. Not examined further.

**What is not established, and what to do next** (nothing here is a decision)

- The rate this stack sustains **with a normal disk**. Run the same `ingest_spike` on a machine where `pg_test_fsync` shows a sync under 2 ms and nothing else is running (a CI runner, a server, a quiet laptop). Until that exists the 20k/s figure of the published run stays the best number and this one says only that the platform does not degrade gracefully when commits are slow: it fails with 5xx after k6's 5 s instead of answering 429.
- Whether `Lock:object` matters when the flush is fast. It is a commit-ordering lock held through the flush, so it should scale with flush time; A/B it on the quiet machine (the relay polls every 50 ms, so the `NOTIFY` is a latency optimisation). The decision whether to keep `pg_notify` in `enqueue_outbox` is a code change and was not made.
- The hot-tenant hypothesis (plan 22 G17's second suspect) was not separated: the spike uses 20 tenants and the steady run 10. `pg_stat_statements` was not enabled and no CPU profile of ingest-svc was taken (steps 4 and 6 below), because the wait events already point at the flush and the commit lock rather than at statements or at the service's CPU.
- Whether an overloaded ingest-svc should answer 429 or 503 quickly instead of holding requests for 5 s and failing them is a design question the finding raises: k6's 5 s bound is the client's, and the error body is `i/o timeout` from the service.

### Steps that find the cause

Run on the largest machine available, one change at a time, with `docker run --rm alpine df -h /` checked first (a full VM disk once produced a false result: [benchmarks.md](benchmarks.md#findings)) and **`pg_test_fsync` second** (a slow disk produces the same symptom, see above).

1. **Reproduce.** `make up`, then `make load S=ingest_spike`; keep the k6 summary. Do it three times; the first run of a fresh stack is not the answer. *Done once, 2026-11-02, on a contended machine (above).*
2. **Is the load generator the limit?** Run k6 on a different machine (or with `K6_ENV` giving more VUs); look at k6's `dropped_iterations` and its own CPU. If dropped iterations vanish and p99 does not move, k6 was not the limit. *Not done: k6 shared the VM; its CPU was not separated.*
3. **Which resource is saturated while the p99 rises?** `docker stats` and the Grafana dashboards (`deploy/grafana`): CPU of ingest-svc, of Postgres, of Redpanda; Postgres connections (`pg_stat_activity`, a pool is 4 connections by default: `pgxx.DefaultMaxConns`, so one ingest-svc may simply be waiting for one: `pgxpool` wait time is in the otelpgx metrics). *Done: services not CPU-bound; a pool of 64 did not help alone.*
4. **Postgres, only if 3 points there.** Enable `pg_stat_statements` on a scratch Postgres (`-c shared_preload_libraries=pg_stat_statements`), repeat step 1, and read the top statements by total time and by `wal_bytes`. Then compare `synchronous_commit=on` with `off` for the same run: if throughput does not change, WAL fsync was not it. If it does, `pg_stat_wal` and `pg_stat_io` say how much of the time is the flush. *Done without `pg_stat_statements`: `synchronous_commit=off` and `pg_stat_wal` with `track_wal_io_timing` (it is a reloadable setting); the flush is it, on this machine.*
5. **A hot tenant partition**, only if one statement dominates: `ingest_spike` uses 20 tenants, `noisy_neighbor` 10. Run `ingest_steady` with one tenant against ten at the same total rate; a difference is the hot partition. *Not done.*
6. **ingest-svc itself, only if 3 points there.** The services expose no profiler. Add one temporary `net/http/pprof` listener on the metrics port in a scratch build, take a 30 s CPU profile at the peak, and remove it. That is the only code change this procedure may need, and only for the run. *Not done: step 3 did not point there.*
7. **Write the result into [benchmarks.md](benchmarks.md#findings) with the artifact**, including a negative one. Only then decide among: more ingest replicas (`hpa` exists, off), a larger Postgres, partitioning, or a change of code. *Written; the decision waits for step 1 on a quiet machine.*

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

2026-11-02 (plan 22 G17): `ingest_spike` at the full 20k events/s profile and four variations of `ingest_steady` at 10k events/s, on the reduced stack and the contended machine described [above](#profile-of-the-ingest-limit-2026-11-02); the other scenarios (`noisy_neighbor`, `entitlement_reads`, `month_close`) and `make bench-idempotency` were **not** run, so their rows in the first table are the published ones. The earlier measurements in this document that are not about throughput are the correctness tests behind the pooler and across two clusters (scaling.md), which measure behaviour.
