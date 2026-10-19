# Benchmarks

Every number here was **measured**, on the machine below, from the commit below, and links to the artifact it came from
(`docs/benchmarks/`). Nothing is a projection. A "target" is what the design aims for ([09-testing-strategy.md](09-testing-strategy.md)); the
last column says whether the measurement met it. Where it did not, that is stated and not rounded away.

## Setup

| | |
|---|---|
| Host | Apple M4, 10 cores, 16 GiB RAM, macOS |
| Docker | OrbStack, Docker 29.8.2, **6 vCPU and 2.9 GiB for the whole VM**, which runs Postgres, Redpanda, RabbitMQ, ClickHouse, Redis, MinIO, the observability stack and every Quotify service (one replica each) next to k6 |
| Code | `origin/main` at `ebc054c` (Phase 08) plus this branch's test tooling; service code is identical |
| Configuration | `deploy/compose/compose.yaml` defaults: containers capped at 96-192 MiB, Postgres `shared_buffers=128MB`, `synchronous_commit=on`, default pool sizes |
| Method | `make load S=<scenario>` (k6 2.3.0 on the compose network), `--summary-export` JSON kept in `docs/benchmarks/`; lag from `consumer_lag_seconds` in Prometheus over the test window |

A laptop VM with 2.9 GiB is far below a CI runner or a server, so these numbers are a floor for the design, not a ceiling. They are
useful for regressions (the same scenarios run in `nightly.yml`) and for comparing the targets honestly.

## Load scenarios: targets vs measured

| Scenario | Target | Measured | Met | Artifact |
|---|---|---|---|---|
| `ingest_steady`: 5,000 events/s in batches of 100, 10 tenants, 3 min (target says 15 min) | p99 < 50 ms, 0 errors, aggregator lag p99 < 5 s | p99 **32.2 ms** (p95 9.5 ms, median 4.6 ms, max 470 ms), **0 errors** of 9,231 requests; aggregator lag p99 **0.02 s** (max 0.02 s), limits lag max 0.02 s; outbox depth max 100 | yes, for 3 min; the 15 min soak is for the nightly | [ingest_steady_5k.json](benchmarks/ingest_steady_5k.json) |
| `ingest_spike`: 1k → 20k events/s over 1 min, held 1 min, 20 tenants | no 5xx, only 429s above quota, recovers in < 2 min | **0 5xx**, 10 answers of 429, 20,994 requests; **p99 2.35 s at the 20k peak** (median 10 ms, p90 0.79 s); 315 iterations dropped by k6 for lack of VUs; aggregator lag max **0.44 s**, limits 0.15 s; outbox depth max 200 | partly: no 5xx and only 429s, but latency degrades sharply at 20k events/s on this VM. Recovery time was not measured separately | [ingest_spike_20k.json](benchmarks/ingest_spike_20k.json) |
| `ingest_spike` on 2 tenants (same ramp: forces the per-tenant quota) | 429s above quota, no 5xx | 14,198 429s, **0 5xx**, p99 16.6 ms (the 429s are cheap) | yes | [ingest_spike_2tenants.json](benchmarks/ingest_spike_2tenants.json) |
| `noisy_neighbor`: tenant A (free tier, quota 100 events/s) sends 1,000 events/s; 9 standard tenants send 100 events/s each | A gets 429s; B-J p99 within 20 % of baseline | A: **1,616 429s**, 185 batches accepted (about 100 events/s, i.e. exactly its quota). Neighbours' p99: **22.7 ms baseline vs 23.3 ms under noise (+2.8 %)** | yes | [noisy_neighbor.json](benchmarks/noisy_neighbor.json) |
| `entitlement_reads`: 2,000 reads/s, 2 min | p99 < 10 ms | p50 0.9 ms, p95 9.1 ms, **p99 23.4 ms**, max 91 ms, 0 errors of 239,966; 300 iterations dropped by k6 | **no** (p95 meets it, p99 does not at 2k/s on this VM) | [entitlement_reads.json](benchmarks/entitlement_reads.json) |
| `month_close`: 10,000 customers x 5 meters, sandbox close-period, until the billing run is COMPLETED | < 5 min | **289.8 s** (10,000 invoices, mock-psp latency 0). The same scenario at 500 customers takes 118 s: about 60 s of that is the run's fixed wait | yes, narrowly | [month_close_10000.json](benchmarks/month_close_10000.json), [month_close_500.json](benchmarks/month_close_500.json) |

## Other measurements

| Question | Target | Measured | Artifact |
|---|---|---|---|
| Gateway RSS growth for a form upload of 20 x 1 MiB files | < 16 MiB | **10.0 MiB** sequential, 6.7 MiB with 4 uploads at once | [assets_and_analytics.txt](benchmarks/assets_and_analytics.txt) |
| assets-svc memory while a 1 GiB file is direct-uploaded, 8 parallel parts | <= 64 MiB | **12.4 MiB** peak (128 parts of 8 MiB in 1.37 s, 746 MiB/s to local MinIO) | same |
| 30-day hourly series of one tenant (3,000,000 events, 720 points) | p95 < 300 ms | **p95 22.0 ms** (p99 26.5 ms); the daily series p95 26.6 ms | same |
| Idempotency table retention, 3M keys ([ADR 0014](adr/0014-idempotency-key-retention.md)) | n/a | dropping a month's partition **24 ms** vs **8.4 s** of batched deletes; inserts 198k vs 113k rows/s | [idempotency_retention.txt](benchmarks/idempotency_retention.txt) |

## Correctness under chaos

`make chaos N=20000` (2 replicas of ingest-svc, aggregator and limits-svc behind Toxiproxy, chaos-tag fault hooks at 10 %, the monkey on, the
workload paced to 100 events/s so that faults land during it), 10 tenants x 20 customers x 5 meters:

| | |
|---|---|
| Faults actually injected | **10 container kills** (aggregator 3, limits-svc 3, billing-svc 3, ingest-svc 1), **2 Toxiproxy faults** (Kafka latency, Redis resets), 1 Redpanda stop (30 s), 1 Redis flush and restart |
| Workload | 20,000 events, 19,976 accepted at first answer, 1,012 duplicate answers (the 5 % duplicates), 63 client retries |
| A1 usage_aggregates == ledger, A2 late_usage == ledger, A3 totals, A4 limits counters == aggregator (after a wiped Redis), A8 ClickHouse FINAL == accepted, A9 DLQs empty | **all pass, zero mismatches** |
| A5 invoice totals, A6 one successful charge per invoice | pass in `TestMonthCloseSurvivesBrokerRestart` (60 customers, RabbitMQ restarted mid-close) |
| A7 frozen-period violations | skipped here (no Prometheus in the chaos stack); `QUOTIFY_PROMETHEUS_URL` enables it |

Artifact: [chaos_20k_local.json](benchmarks/chaos_20k_local.json). **Not yet done:** the 1M-event run and three consecutive green nightlies (the acceptance
criteria of Phase 09) need the nightly workflow to run on a CI runner; `.github/workflows/nightly.yml` is in place and has not run. The fault counts of a
20k-event run are small by construction (it lasts four minutes): the nightly requires at least 30 kills and 10 faults and fails otherwise.

## Findings

- **A first 20k run was discarded.** It showed 5xx and 5 s latencies (`i/o timeout` writing to Postgres). When it was repeated, the Docker VM's disk
  turned out to be 100 % full (`No space left on device`, a Postgres PANIC), which very likely caused it. After freeing the disk and recreating the volumes the
  scenario had no 5xx; the k6 results above are from the clean run. Check `docker run --rm alpine df -h /` before a load run.
- At 20k events/s a single ingest-svc and a single Postgres on this VM queue requests (p99 2.35 s) instead of failing. Where the time goes was not
  profiled; adding ingest replicas is only worth trying after finding out whether Postgres is the bottleneck.
- `entitlement_reads` at 2k/s misses the p99 target by 2x on this VM while the median is under a millisecond. The cause was not investigated;
  re-measure on CI hardware (the nightly runs it) before changing anything.
