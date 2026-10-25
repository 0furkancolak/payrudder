# 09 — Testing strategy

| Level | Tooling | Runs | Scope |
|---|---|---|---|
| Unit | `go test`, `testing/quick` / `pgregory.net/rapid` for property tests | every push | pure logic: rating, period/lateness classification, saga transitions, GCRA math |
| Integration | `testcontainers-go` (Postgres, Redpanda, RabbitMQ, Redis, ClickHouse, MinIO) | every PR | one service + its real dependencies |
| Contract | `buf breaking`, golden proto fixtures, gRPC contract tests | every PR | wire compatibility between services and over time |
| End-to-end | Docker Compose stack + Go test driver (`tests/e2e`) | every PR (`main` merge queue) | full flows: ingest → invoice |
| Load | k6 (`tests/load`) | manual + nightly | throughput, latency, noisy neighbor |
| Chaos / correctness | Go driver + Toxiproxy + container kills (`tests/chaos`) | nightly | exact totals under failure |

## Unit tests (highlights)

- `rating`: table tests for each price model, incremental late-usage pricing, rounding (half-even at line level).
  Properties: total is monotonic in quantity, and `rate(a+b) − rate(a) ≥ 0`.
- `period`: `PeriodOf(t, tz)` and `Classify(period, ingestedAt, cutoff)` around month boundaries, leap years, `23:59:59.999` local time,
  DST transitions and far-offset zones (`Pacific/Kiritimati`, `America/St_Johns`).
- `quantity`: decimal ↔ scaled units round-trip for every scale 0–6, precision rejection, `int64` overflow, no `float64` path (fuzz test on JSON input).
- `payments`: routing table. A `SANDBOX` command never reaches the live provider, whatever the config.
- `saga`: exhaustive `(state × reply)` table. Unknown pairs → no transition and an error.
  Property: random sequences of duplicated or reordered replies never emit two `ChargePayment` with different idempotency keys for the same dunning attempt.
- `gateway/ratelimit`: GCRA against a fake clock.

## Sandbox end-to-end

`tests/e2e/sandbox_test.go`, run on Compose in every PR and on kind in Phase 10 (`make demo-k8s`): create an account → sandbox key →
one customer per `pm_sandbox_*` token → fractional and integer events → `ClosePeriodNow` → assert each saga's terminal state,
`SBX-` numbering, PDF in MinIO, `[SANDBOX]` emails in Mailpit, and zero calls to the live provider.

## Integration tests (per service)

Each service has `internal/.../*_integration_test.go` behind the build tag `integration`. Containers are shared per package
(`TestMain`), and each test gets a fresh database via `CREATE DATABASE … TEMPLATE`.

Required integration tests (also listed in the phase acceptance criteria):

| Service | Test |
|---|---|
| plans-svc | RLS isolation over every table (generated from `pg_policies`). Exclusion constraint on subscriptions |
| ingest-svc | Concurrent duplicate keys → 1 outbox row. Key reuse with a different payload → rejected. Kafka down → outbox drains on recovery |
| aggregator | Crash after commit → no double count. Relay duplicate → skipped. Poison record → DLQ, rest applied. Fencing with two consumers |
| limits-svc | Redelivered batch skipped (offset guard). Thresholds fired once. Reconciliation corrects injected drift |
| billing-svc | Watermark blocks while the outbox is non-empty. Late event → next invoice. Saga happy path with stub workers. `PAYMENT_UNKNOWN` path |
| workers | Redelivered command → same reply, single side effect. TTL retry tiers. Delivery-limit → DLQ |
| analytics-svc | Duplicates merged after `OPTIMIZE … FINAL`. Tenant predicate enforced |

## Contract tests

- `buf lint` + `buf breaking --against '.git#branch=main'` on every PR.
- Golden fixtures `proto/testdata/<message>/<release>.bin` decoded by every consumer's test suite.
- gRPC: each client package has a test that runs against the real server implementation through `bufconn`, so client and server
  can't drift silently in the monorepo.

## Load tests (k6)

`tests/load/` scripts (k6 JS), using `xk6-output-prometheus-remote` so results land in Grafana.

| Scenario | Shape | Pass (**targets**) |
|---|---|---|
| `ingest_steady.js` | 5k events/s, batches of 100, 10 tenants, 15 min | p99 < 50 ms, 0 errors, aggregator lag p99 < 5 s |
| `ingest_spike.js` | 1k → 20k events/s for 2 min | no 5xx, only 429s above quota, recovers in < 2 min |
| `noisy_neighbor.js` | tenant A at 10× quota, B–J nominal | B–J p99 within 20% of steady baseline |
| `entitlement_reads.js` | 2k `CheckEntitlement`/s | p99 < 10 ms |
| `month_close.js` | 10k customers × 5 meters, then trigger close | run COMPLETED < 5 min (excluding mock-psp latency) |

The p99 thresholds of `ingest_steady.js` and `entitlement_reads.js` are the targets above unless `-e P99_MS=<ms>` says otherwise. The nightly runs on a shared
two-vCPU runner with the whole stack beside k6 and passes CI-scale budgets (ingest 300 ms, entitlement 400 ms) because the measured p99 there was 164 ms
(ingest, 2k events/s, median 5.9 ms) and 210 ms (entitlement, 1k reads/s): the targets are for the benchmark VM, the budgets catch an order-of-magnitude regression.

Results are published in `docs/benchmarks.md` (Phase 9) with hardware, commit SHA and raw k6 summaries.
**No number is published that wasn't measured.**

## Chaos / correctness suite

### The headline test: "1M events + random pod kills → exact totals"

**Goal.** Prove the guarantee in [07-correctness.md](07-correctness.md): every accepted event is counted exactly once.

**Setup.**
- Full stack in Docker Compose (or kind in Phase 10), with **2 replicas** each of ingest-svc, aggregator and limits-svc.
- Toxiproxy between services and Postgres, Kafka and Redis.
- Fault hooks compiled in under build tag `chaos` (never in release images):
  `AGG_CRASH_AFTER_COMMIT`, `RELAY_CRASH_AFTER_PRODUCE`, `LIMITS_CRASH_AFTER_LUA`, each a probability.

**Generator** (`tests/chaos/cmd/eventgen`):
- Deterministic seed. 50 tenants × 20 customers × 5 meters (SUM, COUNT, MAX mix).
- **1,000,000 unique events**, sent in batches of 1–500 via the HTTP API.
- **5% intentional duplicates** (same idempotency key, same payload) sent later and concurrently.
- **Client retries**: on timeout/5xx/429, retry the same batch with the same keys, with exponential backoff and no cap on attempts.
- **2% late events**: `event_time` in the previous month, with the test clock (below) placing `ingested_at` past the cutoff.
- **0.1% invalid events** (unknown meter) → expected `REJECTED`, excluded from the totals.
- Writes an expected-totals ledger `(tenant, customer, meter, period, on_time|late) → quantity, count`, counting only events whose
  final response was `accepted` or `duplicate` (the first acceptance).

**Clock control.** Periods are long, so the suite runs with `QUOTIFY_CLOCK_OFFSET` and a configurable grace of 2 minutes
via test-only tenant settings. A "month" is simulated by setting the period boundary a few minutes into the test.

**Chaos schedule** (`tests/chaos/cmd/chaosmonkey`), for the whole duration:
- Every 10–30 s (random): `docker kill -s KILL` one random replica of {ingest-svc, aggregator, limits-svc, outbox relay, billing-svc}.
  Compose restarts it.
- Every 60 s: a Toxiproxy fault for 5–15 s (latency 500 ms, or a connection reset on the Kafka or Postgres proxy).
- Once: stop Redpanda for 30 s. Once: restart Redis without persistence.
- Fault hooks at probability 0.001 per batch.

**Assertions (all must hold exactly).**

| # | Assertion |
|---|---|
| A1 | For every key: `usage_aggregates.quantity` (active generation) == ledger on-time quantity, and `event_count` == ledger count |
| A2 | For every key: `late_usage` == ledger late quantity |
| A3 | Σ ingest `ACCEPTED` responses == Σ `event_count` across aggregates + late usage |
| A4 | After one forced reconciliation: every Redis counter == aggregator quantity (on-time) |
| A5 | After period close: Σ invoice `USAGE` line quantities == A1 totals, and `LATE_USAGE` lines on the next invoice == A2 |
| A6 | mock-psp: at most one successful charge per `(invoice_id, dunning_attempt)` |
| A7 | `aggregator_frozen_period_violations_total == 0` |
| A8 | ClickHouse `usage_events FINAL` totals == A1 + A2 (eventual: polled up to 2 min after drain) |
| A9 | Every DLQ is empty except the injected invalid events, which must **not** be there (they're rejected at ingest) |

**Drain condition.** Generator done, outbox empty, all consumer groups lag 0, all sagas terminal.

**Output.** JSON report (counts, faults injected, durations, any mismatching keys) stored as a CI artifact. A summary goes in
`docs/benchmarks.md`. The test **fails on any single mismatch**. There is no tolerance.

**Runtime target:** < 30 min on a CI runner with 4 vCPU / 16 GB (nightly workflow). A 100k-event variant runs on every PR to `main`.

### How it runs (as built)

`make chaos N=100000` starts the compose stack with `compose.chaos.yaml`: two replicas each of ingest-svc, aggregator and limits-svc,
all built with the `chaos` tag, with their Postgres, Redis and Kafka connections routed through Toxiproxy (Redpanda has a
third, `proxied` listener advertised as `toxiproxy:19093`). `QUOTIFY_CHAOS_MONKEY=1` enables `tests/chaos/monkey.go`, which
kills random replicas, injects Toxiproxy faults, stops Redpanda for 30 s and wipes and restarts Redis, and counts everything
it did; the report (`chaos-report.json`) lists the counts next to A1-A9. Knobs: `QUOTIFY_CHAOS_TENANTS` (default 10, the
nightly uses 50), `QUOTIFY_CHAOS_RATE` (events/s cap, to make a small run last long enough for faults),
`QUOTIFY_CHAOS_MIN_KILLS` / `QUOTIFY_CHAOS_MIN_FAULTS` (fail a run that injected less chaos than required; when the workload ends before the schedule reached them, the monkey keeps going at a faster pace for up to `QUOTIFY_CHAOS_TOPUP_MINUTES`, default 8, while the backlog drains, so the requirement does not depend on the runner's speed), `QUOTIFY_CHAOS_DRAIN_MINUTES` (how long to wait for the aggregator to apply every accepted event, default 40).

A5/A6 are asserted by `TestMonthCloseSurvivesBrokerRestart`, which closes a sandbox period for 60 customers, restarts RabbitMQ
while their sagas are in flight, and then checks every invoice total, `COMPLETED` saga and exactly one successful charge.
A7 needs Prometheus (`QUOTIFY_PROMETHEUS_URL`) and is reported as skipped without it; the report never hides a skip.

### Additional chaos scenarios

- Network partition between aggregator and Postgres mid-transaction → no partial batches.
- Kafka consumer group rebalance storm (scale aggregator 1 → 4 → 1 repeatedly).
- RabbitMQ node restart during the month close → all sagas reach terminal states, no double charge.

## CI matrix (GitHub Actions)

| Workflow | Trigger | Jobs |
|---|---|---|
| `ci.yml` | push, PR | `lint` (golangci-lint, buf lint), `buf-breaking`, `unit`, `integration` (matrix per service, changed-paths filter), `build` (docker images) |
| `e2e.yml` | PR to main | compose up → `tests/e2e` + 100k chaos variant |
| `nightly.yml` | cron 02:00 UTC | 1M chaos suite, k6 steady + noisy neighbor, publish artifacts |
| `release.yml` | tag `v*` | multi-arch images → GHCR, Helm chart package |
