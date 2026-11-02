# 09 — Testing strategy

| Level | Tooling | Runs | Scope |
|---|---|---|---|
| Unit | `go test`, `testing/quick` / `pgregory.net/rapid` for property tests | every push | pure logic: rating, period/lateness classification, saga transitions, GCRA math |
| Integration | `testcontainers-go` (Postgres, Redpanda, RabbitMQ, Redis, ClickHouse, MinIO) | every PR | one service + its real dependencies |
| Contract | `buf breaking`, golden proto fixtures, gRPC contract tests | every PR | wire compatibility between services and over time |
| End-to-end | Docker Compose stack + Go test driver (`tests/e2e`) | every PR and every push to `main` (`e2e` job of `ci.yml`); on kind, `make e2e-kind` (the `kind` job) | full flows: ingest → invoice |
| Load | k6 (`tests/load`) | by hand (`make load S=…`) and the short scenarios of `nightly.yml` | throughput, latency, noisy neighbor |
| Chaos / correctness | Go driver + Toxiproxy + container kills (`tests/chaos`) | 100k events on every PR (`chaos` job of `ci.yml`), 1M events nightly | exact totals under failure |

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

`TestSandboxBillingEndToEnd` and `TestSandboxPaymentScenarios` in `tests/e2e/billing_e2e_test.go`, run on Compose in every PR (`make e2e`) and on kind (`make demo-k8s`, `make e2e-kind`): create an account → sandbox key →
one customer per `pm_sandbox_*` token → events → `POST /v1/sandbox/close-period` → assert each saga's terminal state,
`SBX-` numbering, the PDF in MinIO and its sandbox marker, `[SANDBOX]` receipts in Mailpit, and exactly one charge at the mock provider. `tests/e2e/live_sandbox_e2e_test.go` (`TestLiveAndSandboxAcceptance`) covers the other half: a person signs up and works with the sandbox and live environments of an app.

## Provider sandboxes (nightly)

The adapters' fakes cannot show how a provider really answers, and the compatibility matrices mark those points UNVERIFIED. `TestSandbox` in `pkg/payments/{stripe,paddle,iyzico}` is the one test per adapter that talks to the provider's own sandbox (Stripe: one read-only `GET /v1/balance` with a test-mode key; Paddle: the health check and a cursor-paged list; iyzico: a status query of an unknown key). Without credentials each skips itself with the reason, so `go test` stays green and says nothing about the provider. `.github/workflows/nightly-providers.yml` (a schedule and `workflow_dispatch`) runs them through `scripts/provider-sandbox.sh`, one matrix job per provider: **without the repository secrets the job prints `skipped: no secrets` and succeeds**; with them a failing test fails the job, and so does a test that skipped itself with the secrets set. Secrets: `STRIPE_SANDBOX_API_KEY` (a live key fails the test on purpose), `PADDLE_SANDBOX_API_KEY`, `IYZICO_SANDBOX_API_KEY`, `IYZICO_SANDBOX_SECRET_KEY`, `IYZICO_SANDBOX_CARD`. **None of it has run against a real sandbox: no account exists.** What a green run would and would not prove is the matrices' business; it proves the credentials work and the calls above are accepted, not that a charge, a refund or a webhook behaves as the matrices assume.

## Integration tests (per service)

Each service has `internal/.../*_integration_test.go` behind the build tag `integration`. Containers are shared per package
(`TestMain`), and each test gets a fresh database via `CREATE DATABASE … TEMPLATE`.

Required integration tests (also listed in the phase acceptance criteria):

| Service | Test |
|---|---|
| plans-svc | RLS isolation over every table (generated from `pg_policies`). Exclusion constraint on subscriptions |
| ingest-svc | Concurrent duplicate keys → 1 outbox row. Key reuse with a different payload → rejected. Kafka down → outbox drains on recovery |
| aggregator | Crash after commit → no double count. Relay duplicate → skipped. Poison record → DLQ, rest applied. Fencing with two consumers. Hour, day, ISO-week, month, year and lifetime reads of `GetUsageBatch` equal the on-time events of each window (three time zones, sum and max meters); hour totals pruned by retention and carried over by a replay |
| limits-svc | Redelivered batch skipped (offset guard). Thresholds fired once. Reconciliation corrects injected drift, also of hour, day, year and lifetime counters (a lifetime counter never expires). Window boundaries and DST in `pkg/period` unit tests |
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
- Fault hooks compiled in under build tag `chaos` (the images built with `GO_TAGS=chaos`, which `compose.chaos.yaml` does, are the only ones that have them):
  `AGG_CRASH_AFTER_COMMIT`, `RELAY_CRASH_AFTER_PRODUCE`, `LIMITS_CRASH_AFTER_LUA`, each a probability.

**Generator** (`generate` in `tests/chaos/chaos_test.go`):
- Deterministic seed (`PAYRUDDER_CHAOS_SEED`, default 42). `PAYRUDDER_CHAOS_TENANTS` tenants (default 10; the nightly uses 50) × 20 customers × 5 meters (SUM, COUNT, MAX mix).
- `PAYRUDDER_CHAOS_EVENTS` unique events (default 100,000; the nightly sends 1,000,000), sent in batches of 1–500 via the HTTP API.
- **5% intentional duplicates** (same idempotency key, same payload) sent later and concurrently.
- **Client retries**: on timeout/5xx/429, retry the same batch with the same keys, with a backoff, until a 25-minute deadline per batch.
- **2% late events**: `event_time` in the previous month (at most three days before it started); the test tenants have a grace of 0, so such an event is late the moment it arrives.
- **0.1% invalid events** (unknown meter) → expected `REJECTED`, excluded from the totals.
- Writes an expected-totals ledger `(tenant, customer, meter, period, on_time|late) → quantity, count`, counting only events whose
  final response was `accepted` or `duplicate` (the first acceptance).

**Clock.** There is no clock control. The suite creates its tenants with a grace of 0 (`setup` in `tests/chaos/chaos_test.go`) and sends late events with an `event_time` in the previous month, so lateness needs no clock games. The period close is not part of this run: the live tenants never close a period (A5 and A6 are asserted by `TestMonthCloseSurvivesBrokerRestart`, below).

**Chaos schedule** (`tests/chaos/monkey.go`), for the whole duration:
- Every 10–30 s (random): kill one random replica of {ingest-svc, aggregator, limits-svc, billing-svc} (the outbox relay runs inside ingest-svc and billing-svc). The monkey brings it back 2–5 s later.
- Every 60 s: a Toxiproxy fault for 5–15 s (latency of 500 ms or a connection reset on the Postgres, Kafka or Redis proxy; on Kubernetes a deny-all NetworkPolicy).
- Once: stop Redpanda for 30 s. Once: restart Redis without persistence.
- Fault hooks: `compose.chaos.yaml` sets `RELAY_CRASH_AFTER_PRODUCE` and `AGG_CRASH_AFTER_COMMIT` to 0.1 by default and leaves `LIMITS_CRASH_AFTER_LUA` and `BILLING_RELAY_CRASH_AFTER_PUBLISH` at 0 (each is an environment variable).

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

**Runtime target:** < 30 min on a CI runner with 4 vCPU / 16 GB. The nightly job runs on `ubuntu-latest` with a 150-minute timeout; GitHub shows its `chaos-1m` job passing in 35 to 45 minutes on the nights of 2026-10-30 to 11-02 (the reports of those runs are artifacts and are not copied into [benchmarks.md](benchmarks.md#correctness-under-chaos), which lists what was measured by hand). The 100k-event variant runs in the `chaos` job of `ci.yml` on every pull request and every push to `main`.

### How it runs (as built)

`make chaos N=100000` starts the compose stack with `compose.chaos.yaml`: two replicas each of ingest-svc, aggregator and limits-svc,
all built with the `chaos` tag, with their Postgres, Redis and Kafka connections routed through Toxiproxy (Redpanda has a
third, `proxied` listener advertised as `toxiproxy:19093`). `PAYRUDDER_CHAOS_MONKEY=1` enables `tests/chaos/monkey.go`, which
kills random replicas, injects Toxiproxy faults, stops Redpanda for 30 s and wipes and restarts Redis, and counts everything
it did; the report (`chaos-report.json`) lists the counts next to A1-A9. Knobs: `PAYRUDDER_CHAOS_TENANTS` (default 10, the
nightly uses 50), `PAYRUDDER_CHAOS_RATE` (events/s cap, to make a small run last long enough for faults),
`PAYRUDDER_CHAOS_MIN_KILLS` / `PAYRUDDER_CHAOS_MIN_FAULTS` (fail a run that injected less chaos than required; when the workload ends before the schedule reached them, the monkey keeps going at a faster pace for up to `PAYRUDDER_CHAOS_TOPUP_MINUTES`, default 8, while the backlog drains, so the requirement does not depend on the runner's speed), `PAYRUDDER_CHAOS_DRAIN_MINUTES` (how long to wait for the aggregator to apply every accepted event, default 40).

The nightly workflow runs both of its jobs with `PAYRUDDER_SAAS=off`: the compose stack sells Payrudder's own plans by default, and the Free plan refuses an account's events after 10,000 a month, which a load test or a 1M-event run exceeds by design.

A5/A6 are asserted by `TestMonthCloseSurvivesBrokerRestart`, which closes a sandbox period for 60 customers, restarts RabbitMQ
while their sagas are in flight, and then checks every invoice total, `COMPLETED` saga and exactly one successful charge.
A7 needs Prometheus (`PAYRUDDER_PROMETHEUS_URL`) and is reported as skipped without it; the report never hides a skip.

### Additional chaos scenarios

- Network partition between aggregator and Postgres mid-transaction → no partial batches. *Covered only as the monkey's Toxiproxy faults (above) during the main run, not as a scenario of its own.*
- Kafka consumer group rebalance storm (scale aggregator 1 → 4 → 1 repeatedly). *Not built: no test scales the aggregator.*
- RabbitMQ node restart during the month close → all sagas reach terminal states, no double charge. *Built as `TestMonthCloseSurvivesBrokerRestart` (one RabbitMQ container restarted; compose has no RabbitMQ cluster).*

## CI matrix (GitHub Actions)

What exists in `.github/workflows/`. Nothing here builds or pushes container images, packages the Helm chart or publishes anything: there is no release workflow (the owner's rule is no releases until the app is complete, and `sdk-publish.yml` is a manual dry run).

**`ci.yml`** runs on every pull request and every push to `main`. A pull request's older run is cancelled by its next push; every commit on `main` has a concurrency group of its own (the sha), so a merge's run is not replaced by the next merge's. Jobs, with the `name:` GitHub shows:

| Job | What it runs |
|---|---|
| `static` (lint, buf, generated code) | `make lint` (golangci-lint on every module, `buf lint`), `buf breaking` against the base, and `make gen` followed by `git diff --exit-code` (generated code is up to date) |
| `test` (unit tests) | `make test` (`go test -race` on every module) and `make test-rules` (Prometheus alert rules, Alertmanager routes) |
| `integration` (integration tests) | `make test-integration`: every module with `-tags integration`, containers through testcontainers |
| `compose` (compose stack healthy) | `make up-infra` with every container healthy |
| `e2e` (end-to-end, compose) | `make up`, `make e2e` (`tests/e2e`), the console's Playwright smoke test, `make mem-budget`, `make seed` |
| `web` (operator console) | lint, typecheck, unit tests, the generated API types match `api/openapi/v1.yaml`, build, Playwright against a mocked gateway |
| `hosted-pages` (customer-facing pages) | the gateway writes every hosted page in every language; axe-core on each, light and dark, at three widths |
| `backup` (backup scripts and recovery drill) | `make test-backup-scripts`, `make test-store-backups`, `make backup-drill` |
| `helm` (helm charts) | `make helm-lint`, the render tests in `tests/helm`, `ct lint` |
| `kind` (kubernetes) | `kind-up`, `kind-images`, `helm-install`, `netpol-test`, `e2e-kind`, `rollout-test` under k6 load |
| `chaos` (correctness, 100k events) | `make chaos N=100000` with the chaos monkey on; the report is an artifact |

**Other workflows.**

| Workflow | Trigger | What it runs |
|---|---|---|
| `nightly.yml` | cron `17 2 * * *` (02:17 UTC), `workflow_dispatch` | `chaos-1m` (the 1M-event correctness run, 50 tenants, `PAYRUDDER_CHAOS_MIN_KILLS=30`, `MIN_FAULTS=10`), `k6` (`ingest_steady`, `entitlement_reads`, `noisy_neighbor` at CI-scale budgets) and `cells` (the integration suites of `pkg`, ingest-svc, payments-worker, plans-svc, aggregator and billing-svc in cells mode, with and without PgBouncer); all with `PAYRUDDER_SAAS=off` |
| `nightly-providers.yml` | cron `43 3 * * *`, `workflow_dispatch` | `TestSandbox` of each payment adapter (see above) |
| `sdk-go.yml`, `sdk-typescript.yml`, `sdk-python.yml`, `sdk-php.yml` | path filters, push to `main` and pull request | each SDK's lint, type check, tests and build, its generated code is up to date with `api/openapi/v1.yaml` (the Go SDK also has a contract test) |
| `sdk-release-check.yml` | path filters | one version everywhere, LICENSE and CHANGELOG, registry metadata; publishes nothing |
| `sdk-publish.yml` | `workflow_dispatch` only | the one way an SDK could leave the repository; a dry run by default, not used |
| `snippets.yml`, `snippets-run.yml` | path filters | the console's quick-start code compiled against the SDKs, and run against `docs-site/fakeapi.mjs` |
| `examples.yml` | path filters | the framework templates under `examples/` compiled and smoke-tested against the same fake API |
| `mcp.yml` | path filters | the MCP server's tools and resources generated from `api/openapi/v1.yaml` and the public documents |
| `einvoice-schematron.yml` | path filters | the generated UBL against the EN 16931, Peppol BIS 3 and UBL-TR rules (`make test-einvoice-rules`) |
| `docs.yml` | path filters, `workflow_dispatch` | builds and checks the developer site; deploys it to GitHub Pages only when the repository variable `DOCS_PUBLISH` is `true` |
| `deploy-console.yml` | push to `main` under `web/admin`, `workflow_dispatch` | builds the console; deploys to Cloudflare Workers only with the Cloudflare secrets |

Whether a workflow's last run on `main` was green is not something this page can say; read it with `gh run list --branch main` ([plan 01](plans/01-baseline-and-review.md) says how the owner reads it).
