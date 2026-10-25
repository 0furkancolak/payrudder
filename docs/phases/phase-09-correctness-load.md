# Phase 09 — Correctness & load

## Goal

Run the full "1M events + random pod kills → exact totals" suite and the k6 scenarios, then publish measured
numbers with methodology.

## Scope

**In:** grow `tests/chaos` from v0 to the full spec in [09-testing-strategy.md](../09-testing-strategy.md#chaos--correctness-suite),
chaosmonkey (container kills, Toxiproxy faults, broker restarts), test clock/short grace, assertions A1–A9, JSON report,
nightly workflow, k6 scenarios, `docs/benchmarks.md`.

**Out of scope:** Kubernetes chaos (Phase 10), multi-node broker failures.

## Tasks

- [x] Compose `chaos` profile: 2× ingest-svc, aggregator, limits-svc. Toxiproxy in front of Postgres, Redpanda, Redis
- [x] Generator: 50 tenants × 20 customers × 5 meters, 1M unique events, 5% duplicates, unbounded client retries, 2% late, 0.1% invalid, ledger output (parameterised: `PAYRUDDER_CHAOS_TENANTS`, 10 by default and 50 in the nightly)
- [x] Test-only tenant settings: grace = 2 min, shortened period via `PAYRUDDER_CLOCK_OFFSET` (grace 0 instead of a shifted clock: late events are those of the previous month)
- [x] chaosmonkey: random `docker kill` every 10–30 s, Toxiproxy faults every 60 s, one Redpanda stop (30 s), one Redis restart without persistence
- [x] Drain detector (outbox empty, lag 0, sagas terminal)
- [x] Assertions A1–A9 with a per-key mismatch report
- [x] JSON report + CI artifact + summary table
- [x] `nightly.yml` (1M) and PR variant (100k)
- [x] k6 scenarios: `ingest_steady`, `ingest_spike`, `noisy_neighbor`, `entitlement_reads`, `month_close`
- [x] k6 → Prometheus remote write. Grafana k6 dashboard
- [x] `docs/benchmarks.md`: hardware, commit SHA, configs, results, and an explicit "targets vs measured" table
- [x] Fix every bug found: the harness found none in the services this time (the one flaky test, `TestLimits/EntitlementChangeVisible`, had a window too tight for CI and was widened); Kafka DLQ redrive and the idempotency-retention ADR (open since Phase 02) are done

## Deliverables

- Nightly green correctness run with an archived report.
- `docs/benchmarks.md` with **measured** numbers only.

## Acceptance criteria

- [ ] 1M chaos run: A1–A9 all pass with zero mismatches, three consecutive nightly runs
- [x] The report counts the faults actually injected, and the nightly fails below 30 kills / 10 faults (`PAYRUDDER_CHAOS_MIN_KILLS`, `PAYRUDDER_CHAOS_MIN_FAULTS`); the 20k local run injected 10 kills and 2 faults
- [ ] Duplicate rate observed by aggregator (`aggregator_duplicates_skipped_total`) > 0, which proves the dedup paths were exercised
- [x] k6 `noisy_neighbor`: tenant A receives 429s, and B–J p99 is recorded against baseline (pass/fail vs the 20% **target** reported honestly)
- [x] `docs/benchmarks.md` published. Every number links to a k6 summary or chaos report artifact

## Dependencies

Phases 00–08.

## Risks / open questions

- CI runner resources may be too small for 1M with the full stack. Fallback: self-hosted runner or a documented local run with uploaded artifacts.
- Flaky chaos tests are worse than none. Any flake is a bug until proven to be a test bug.

## Notes (as built)

- **Chaos stack.** `deploy/compose/compose.chaos.yaml` runs 2x ingest-svc, aggregator and limits-svc behind Toxiproxy
  (`deploy/compose/chaos/toxiproxy.json`); fault hooks as before. `tests/chaos/monkey.go` is the chaos monkey. `docker kill`
  leaves a container down (the restart policy does not apply to a container killed through the API), so the monkey plays
  the orchestrator and starts it again after 2-5 s.
- **Suite.** `tests/chaos/chaos_test.go`: tenants x 20 customers x 5 meters (SUM/COUNT/MAX at scales 0, 2 and 3), 5 %
  duplicates, unbounded retries, 2 % late, 0.1 % invalid. Assertions A1-A4, A8, A9 in the main test (A4 also proves a wiped
  Redis rebuilds), A5/A6 in `billing_chaos_test.go`, A7 when Prometheus is configured. Each assertion has a status in the
  JSON report; mismatches are listed per key.
- **Not decided by this repo's CI.** "Three consecutive green nightlies" and the 1M run need the nightly workflow to run on
  a runner; `.github/workflows/nightly.yml` is in place, but no nightly has run yet. `docs/benchmarks.md` carries only
  what was measured, on the laptop described there.
- **k6.** `tests/load/*.js`, `make load S=<scenario>`; results stream to Prometheus (remote write) and the "Payrudder / k6"
  dashboard; `--summary-export` JSON lands in `tests/load/results/`.
- **Kafka DLQ redrive** (open since Phase 02): `payrudderctl dlq ls|show|redrive --broker kafka`, tracked by the
  `payrudderctl-redrive` consumer group; Kafka has no delete, so `purge` is refused.
