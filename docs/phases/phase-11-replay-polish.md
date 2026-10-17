# Phase 11 — Replay, re-rating & polish

## Goal

Support safe re-rating of closed periods after a price change and re-aggregation of history into a new aggregate
generation, then polish the repo into a portfolio piece with a scripted demo.

## Scope

**In:** aggregator `ReplayService` (generations, replay consumer group, verification diff, promotion with catch-up),
billing-svc `StartRerating` (delta → correction invoice or credit balance, credit applied on the next invoice),
`quotifyctl replay|rerate`, demo script, README overhaul, architecture diagram export, final docs pass.

**Out of scope:** automated replay triggers, partial-tenant generations (replay is per tenant or all, and promotion is global per generation).

## Tasks

- [ ] `StartReplay`: create generation N+1 (`building`), compute start offsets via `ListOffsetsAfterMilli(start(from) − 35d)`, snapshot end offsets
- [ ] Replay consumer: same apply code path, generation-parameterized, its own consumer group and offsets, tenant/period filter
- [ ] Verification: per-(customer, meter, period) diff N vs N+1 stored on the job
- [ ] `PromoteGeneration`: pause live, catch replay up to live offsets, flip `active`, resume. Generation-pinned billing runs unaffected
- [ ] `StartRerating`: require COMPLETED run, pin generation, rate with the new plan version, compute delta vs already billed
- [ ] `CORRECTION` invoice through the normal saga. `credit_balances` + `CREDIT` line on the next regular invoice
- [ ] Idempotency: unique `(tenant, period, plan_version)` on `rerating_jobs`
- [ ] `quotifyctl replay start|status|promote`, `quotifyctl rerate`
- [ ] `make demo`: seed → traffic → close → change price → re-rate → show correction invoice + email + Grafana links
- [ ] README: pitch, architecture diagram, "correctness in 60 seconds", benchmark summary, how to run
- [ ] Docs consistency pass (every doc matches the code). Mark the roadmap status

## Deliverables

- Replay and re-rating operable via CLI and documented.
- Polished README with demo GIF/asciicast and benchmark summary.

## Acceptance criteria

- [ ] Integration: re-rating a period with a 20% cheaper price → credit balance equal to the exact expected delta, applied as a `CREDIT` line on the next invoice
- [ ] Integration: re-rating with a higher price → one `CORRECTION` invoice for the exact delta, saga completes
- [ ] Integration: the same re-rating requested twice → one job, one correction
- [ ] Integration: a meter-filter bug fixed via a new meter version + replay → generation N+1 totals equal the corrected expectation. Promotion happens with live traffic flowing and no lost or double-counted live events (chaos-v0 style ledger check)
- [ ] Integration: a billing run pinned to generation N re-reads identical totals after N+1 is promoted
- [ ] `make up && make demo` completes in < 5 min on a laptop (success criterion S6)

## Dependencies

Phase 03 (generation-aware schema), Phase 05 (rating, saga), Phase 09 (ledger tooling).

## Risks / open questions

- Promotion pause duration under heavy traffic: measure. Bound with a max catch-up lag before allowing promotion.
- Replay of all tenants vs. one tenant with a global generation: with per-tenant replays, a global flip also "replays" untouched tenants
  (copied). Alternative: per-tenant active generation. **Needs a decision before Phase 03 locks the schema.**
