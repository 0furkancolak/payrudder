# Phase 05 — billing-svc: period close, late events, saga

## Goal

Close each tenant's month deterministically once the watermark proves all on-time usage is aggregated, rate it into
invoices (including late-usage adjustments), and drive every invoice through an orchestrated, crash-safe saga, tested
against stub workers.

## Scope

**In:** `billing_db` schema, scheduler + advisory locks, watermark protocol, `rating` package, invoice drafting/finalization
with gap-free numbering, saga state machine + transitions + sweeper + dunning scheduling, `commands.v1` protos, `pkg/outbox`
AMQP sink, `pkg/rabbitx` (connection, topology, confirms), reply consumer, `billing.invoices.v1` facts, **stub worker** for tests,
`BillingService` read APIs, `ClosePeriod`, `RetryInvoiceSaga`, `VoidInvoice`.

**Out of scope:** real workers (Phase 06), re-rating (Phase 11), credit balances beyond the schema.

## Tasks

- [x] `proto/payrudder/commands/v1` (envelopes + payloads), `proto/payrudder/billing/v1`, `InvoiceFact` event
- [x] Migrations per [05-data-model.md](../05-data-model.md#billing_db)
- [x] `rating` package: PER_UNIT, TIERED_GRADUATED, PACKAGE, FLAT. `apd` decimals, scaled-unit → decimal conversion, currency exponent table. Half-even line rounding. Incremental late pricing
- [x] Property tests for rating (monotonicity, non-negative increments)
- [x] Scheduler: find due `(tenant, period)` (`now ≥ cutoff + 30 s`, cutoff from tenant-time-zone period bounds), advisory lock, `billing_runs` state machine
- [x] Watermark: `GetOutboxWatermark` → end-offset snapshot → `GetIngestWatermark` until caught up
- [x] Stream usage + late usage + subscriptions. Draft invoices in pages of 500. Pin generation
- [x] `FreezePeriod` call after rating
- [x] Finalize: `invoice_number_seq … FOR UPDATE`, `INV-{year}-{seq:06}` (live), `SBX-{year}-{seq:06}` (sandbox)
- [x] `SandboxService.ClosePeriodNow` → `aggregator.ClosePeriodEarly` → normal run. Compressed dunning for sandbox
- [x] `CommandEnvelope.environment` set from the tenant on every command
- [x] Saga engine: transition table, guarded `UPDATE`, `saga_events`, outbox command with `uuidv5` command ID
- [x] `pkg/rabbitx`: topology declaration from one Go definition, publisher confirms, consumer with manual ack
- [x] `pkg/outbox` AMQP sink (same relay, second sink)
- [x] Reply consumer with `inbox_replies` dedup
- [x] Sweeper for `step_deadline` (`SKIP LOCKED`), attempt cap → `*_FAILED` / `PAYMENT_UNKNOWN`
- [x] Dunning scheduler (`next_attempt_at`: +1d, +3d, +7d) with a test clock
- [x] `tests/stubworker`: consumes all command queues, replies per a configurable script (success, fail, timeout, duplicate reply)
- [x] Admin RPCs: `ClosePeriod`, `GetBillingRun`, `RetryInvoiceSaga`, `VoidInvoice`
- [x] `billing.invoices.v1` facts via outbox

## Deliverables

- billing-svc image. A period close produces invoices that reach `COMPLETED` with the stub worker.
- Saga transition table rendered in docs (generated from code, kept in sync by a test).

## Acceptance criteria

- [x] Unit: every `(state, reply)` pair in the transition table. Invalid pairs are rejected
- [x] Property: random reply sequences with duplicates and reorders → at most one `ChargePayment` per `(invoice, dunning_attempt)`
- [x] Integration: billing run stays `WAITING_WATERMARK` while the ingest outbox holds an on-time event, and proceeds after it drains
- [x] Integration: event ingested at `cutoff + 1 s` for the closed period → `LATE_USAGE` line on the next period's invoice, original invoice unchanged
- [x] Integration: tiered price, late usage crossing a tier → incremental amount matches the hand-computed value
- [x] Integration: billing-svc killed after a transition commit → the command is still published once, and the saga completes
- [x] Integration: duplicate `PaymentSucceeded` reply → single transition, second ignored (metric + `saga_events` show one)
- [x] Integration: stub worker never replies → sweeper retries 3×, then `PDF_FAILED`. `RetryInvoiceSaga` resumes it
- [x] Integration: `PaymentErrored` → `PAYMENT_UNKNOWN` → status query → `PAID` without a second charge command
- [x] Integration: `ClosePeriod` called twice → one billing run, one invoice per customer
- [x] Invoice numbers are gap-free under 20 concurrent finalizations (test)
- [x] Integration: sandbox `ClosePeriodNow` mid-month → invoice `SBX-…` reaches `COMPLETED` with the stub worker within 2 min. The same call with a live key → `403`
- [x] Unit: fractional usage `1.2345` GB-h × `"12.5"` minor units → 15.43125 → `15` minor units (half-even at line level; table tests also pin the `.5` cases 0.5→0, 1.5→2, 2.5→2). Unit prices are minor units per ADR 0010, so the amount is the same integer in `TRY` (0.15) and `JPY` (15)

## Dependencies

Phase 03 (usage + watermark APIs), Phase 02 (outbox watermark), Phase 01 (subscriptions, plan versions). Phase 04 not required.

## Risks / open questions

- This is the largest phase. If it runs long, move dunning to Phase 06 (keeping `PAYMENT_DECLINED → UNCOLLECTIBLE` direct).
- Zero-usage customers with only a base fee: invoice them (planned: yes, if the plan has `base_fee_minor > 0`).
- Should invoice snapshot data go in the command (planned) or should workers fetch it via gRPC? A larger message, but workers stay decoupled.

## Notes (as built)

- **Packages.** `internal/rating` (pure, `apd`), `internal/saga` (the transition table in `table.go` is the single source:
  exhaustive/guard-determinism tests, and the diagram in `docs/03-services/billing-svc.md` is generated from it by
  `UPDATE_DOCS=1 go test ./internal/saga`), `internal/runs` (scheduler, watermark, rating, finalization), `internal/replies`,
  `internal/api` (three gRPC services). SQL is hand-written pgx rather than sqlc: the saga and rating queries are dynamic
  enough that generated code added friction.
- **Outbox.** billing's outbox has the shared `pkg/outbox` shape plus `message_id UNIQUE`; `x-destination` (`rabbitmq` |
  `kafka`) routes a row, so one relay drains commands and `InvoiceFact`s (`outbox.Router`, `outbox.AMQPSink`). Issuing the same
  `(saga, step, attempt)` twice is a no-op; a relay crash after the broker ack republishes and workers dedup on `command_id`.
- **Attempts.** `attempt` is monotonic per saga (part of every command id); `step_attempt` counts issues of the current
  step (cap 3). The charge's PSP idempotency key `invoice:charge:{dunning_attempt}` stays the same across re-issues and the
  re-charge after `STATUS_NOT_FOUND`.
- **Late usage** is priced as `rate(base + prior late + new) − rate(base + prior late)` where `base` is the origin
  period's on-time aggregate and `prior late` the quantity of earlier `LATE_USAGE` lines of the same origin.
- **Tenant discovery** uses a new operator RPC `AdminService.ListTenants`; the scheduler creates runs for the last 3 closed
  periods of each tenant (since its creation). A plan change inside a period is billed at the later plan (no proration).
- **Deviation from the task list:** the stub worker lives in `pkg/stubworker` (importable from every module's tests), not
  `tests/stubworker`; `pkg` now depends on `gen/go` for the typed envelopes.
- Facts: `InvoiceFinalized` (v1), `InvoicePaid` (v2) and `InvoiceVoided` (v3) go to `billing.invoices.v1`.
- Not built here: credits/`credit_balances` use (Phase 11 re-rating), the `GetInvoicePdfUrl` RPC (needs the invoice worker's
  bucket, Phase 06), `RetryRun` through the HTTP API beyond `ClosePeriod` re-driving a FAILED run.
