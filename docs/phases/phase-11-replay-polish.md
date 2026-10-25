# Phase 11 — Replay, re-rating & polish

## Goal

Support safe re-rating of closed periods after a price change and re-aggregation of history into a new aggregate
generation, then polish the repo into a portfolio piece with a scripted demo.

## Scope

**In:** aggregator `ReplayService` (per-tenant generations, replay consumer group, verification diff, promotion with dual-write catch-up),
billing-svc `StartRerating` (delta → correction invoice or credit balance, credit applied on the next invoice),
`payrudderctl replay|rerate`, demo script, README overhaul, architecture diagram export, final docs pass.

**Out of scope:** automated replay triggers, replays narrower than one tenant (e.g. one customer).

## Tasks

- [x] `StartReplay(tenant)`: create `(tenant, N+1, building)`, compute start offsets via `ListOffsetsAfterMilli(start(from) − 35d)`, snapshot end offsets. (One tenant per job; "all tenants" is a loop of calls, there is no batch endpoint)
- [x] Replay consumer: same apply code path, generation-parameterized, its own consumer group and offsets, tenant/period filter
- [x] Verification: per-(customer, meter, period) diff N vs N+1 stored on the job
- [x] `catching_up`: live consumer dual-writes the tenant into N and N+1 and records `dual_write_from`. Replay stops there
- [x] `PromoteGeneration`: per-tenant flip in one transaction, no pause of live traffic. `DiscardGeneration`. Generation-pinned billing runs unaffected
- [x] `StartRerating`: require COMPLETED run, pin generation, rate with the new plan version, compute delta vs already billed
- [x] `CORRECTION` invoice through the normal saga. `credit_balances` + `CREDIT` line on the next regular invoice
- [x] Idempotency: unique `(tenant, period, plan_version)` on `rerating_jobs`
- [x] `payrudderctl replay start|status|promote`, `payrudderctl rerate`
- [x] `make demo`: seed → traffic → close → change price → re-rate → show correction invoice + email + Grafana links
- [x] README: pitch, architecture diagram, "correctness in 60 seconds", benchmark summary, how to run (the demo is the captured output of `make demo`, not a GIF)
- [x] Docs consistency pass (every doc matches the code). Mark the roadmap status. (Done for the pieces this phase touched and for the stale statements found by grep: `make load`, `make demo`, the status lines; there is no tool that proves every sentence true.)

## Deliverables

- Replay and re-rating operable via CLI and documented.
- Polished README with demo GIF/asciicast and benchmark summary.

## Acceptance criteria

- [x] Integration: re-rating a period with a 20% cheaper price → credit balance equal to the exact expected delta, applied as a `CREDIT` line on the next invoice
- [x] Integration: re-rating with a higher price → one `CORRECTION` invoice for the exact delta, saga completes
- [x] Integration: the same re-rating requested twice → one job, one correction
- [x] Integration: a meter-filter bug fixed via a new meter version + replay → generation N+1 totals equal the corrected expectation. Promotion happens with live traffic flowing and no lost or double-counted live events (chaos-v0 style ledger check)
- [x] Integration: a billing run pinned to generation N re-reads identical totals after N+1 is promoted
- [x] Integration: replay + promotion of tenant A leaves tenant B's rows and generation untouched, and B's live events are counted exactly (B's ingest p99 during a replay was not recorded)
- [x] `make up && make demo` completes in < 5 min on a laptop (success criterion S6)

## Dependencies

Phase 03 (generation-aware schema), Phase 05 (rating, saga), Phase 09 (ledger tooling).

## Risks / open questions

- Promotion pause duration under heavy traffic: measure. Bound with a max catch-up lag before allowing promotion.
- **Decided:** per-tenant active generation ([ADR 0012](../adr/0012-per-tenant-aggregate-generations.md)), schema in Phase 03.
- Dual-write doubles write load for the replayed tenant during catch-up. Bound by allowing one open replay per tenant.


## Notes (as built)

- **Replay** is `services/aggregator/internal/replay` plus a `Replay` setting on the live `apply.Handler`; the design, the flip into dual write and the
  READ COMMITTED bug the integration test found are written up in [07-correctness.md](../07-correctness.md#8-replay--re-rating) and the addendum to
  [ADR 0012](../adr/0012-per-tenant-aggregate-generations.md). `replay_integration_test.go` runs a tenant through a filter fix, replay, verification and promotion
  while live traffic flows for it and for a bystander, and compares the promoted generation with a ledger of every event.
- **Re-rating** is `services/billing-svc/internal/runs/rerate.go` (jobs, corrections, credits) and `applyCredits` in `rate.go`; see
  [billing-svc.md](../03-services/billing-svc.md#re-rating). Migration `00002_rerating.sql` adds the job counters, the per-job uniqueness and `credit_applications`.
- **Operator surface**: admin routes in the gateway and OpenAPI, `payrudderctl replay start|status|promote|discard`, `payrudderctl rerate`, and
  `e2e/replay_e2e_test.go` against the Compose stack (start, 409 on a second open replay, verification with no differences, promote, keep counting, discard).
- **`make demo`** is `payrudderctl demo`: 2 minutes 24 seconds on the laptop of [benchmarks.md](../benchmarks.md), with billing-svc's sandbox dunning and scheduler
  put on seconds by the target.
- Not done: replaying a *list* of tenants in one call, narrower-than-tenant replays (out of scope), and a recorded GIF/asciicast.
