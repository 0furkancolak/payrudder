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

- [ ] `proto/quotify/commands/v1` (envelopes + payloads), `proto/quotify/billing/v1`, `InvoiceFact` event
- [ ] Migrations per [05-data-model.md](../05-data-model.md#billing_db)
- [ ] `rating` package: PER_UNIT, TIERED_GRADUATED, PACKAGE, FLAT. `apd` decimals, scaled-unit → decimal conversion, currency exponent table. Half-even line rounding. Incremental late pricing
- [ ] Property tests for rating (monotonicity, non-negative increments)
- [ ] Scheduler: find due `(tenant, period)` (`now ≥ cutoff + 30 s`, cutoff from tenant-time-zone period bounds), advisory lock, `billing_runs` state machine
- [ ] Watermark: `GetOutboxWatermark` → end-offset snapshot → `GetIngestWatermark` until caught up
- [ ] Stream usage + late usage + subscriptions. Draft invoices in pages of 500. Pin generation
- [ ] `FreezePeriod` call after rating
- [ ] Finalize: `invoice_number_seq … FOR UPDATE`, `INV-{year}-{seq:06}` (live), `SBX-{year}-{seq:06}` (sandbox)
- [ ] `SandboxService.ClosePeriodNow` → `aggregator.ClosePeriodEarly` → normal run. Compressed dunning for sandbox
- [ ] `CommandEnvelope.environment` set from the tenant on every command
- [ ] Saga engine: transition table, guarded `UPDATE`, `saga_events`, outbox command with `uuidv5` command ID
- [ ] `pkg/rabbitx`: topology declaration from one Go definition, publisher confirms, consumer with manual ack
- [ ] `pkg/outbox` AMQP sink (same relay, second sink)
- [ ] Reply consumer with `inbox_replies` dedup
- [ ] Sweeper for `step_deadline` (`SKIP LOCKED`), attempt cap → `*_FAILED` / `PAYMENT_UNKNOWN`
- [ ] Dunning scheduler (`next_attempt_at`: +1d, +3d, +7d) with a test clock
- [ ] `tests/stubworker`: consumes all command queues, replies per a configurable script (success, fail, timeout, duplicate reply)
- [ ] Admin RPCs: `ClosePeriod`, `GetBillingRun`, `RetryInvoiceSaga`, `VoidInvoice`
- [ ] `billing.invoices.v1` facts via outbox

## Deliverables

- billing-svc image. A period close produces invoices that reach `COMPLETED` with the stub worker.
- Saga transition table rendered in docs (generated from code, kept in sync by a test).

## Acceptance criteria

- [ ] Unit: every `(state, reply)` pair in the transition table. Invalid pairs are rejected
- [ ] Property: random reply sequences with duplicates and reorders → at most one `ChargePayment` per `(invoice, dunning_attempt)`
- [ ] Integration: billing run stays `WAITING_WATERMARK` while the ingest outbox holds an on-time event, and proceeds after it drains
- [ ] Integration: event ingested at `cutoff + 1 s` for the closed period → `LATE_USAGE` line on the next period's invoice, original invoice unchanged
- [ ] Integration: tiered price, late usage crossing a tier → incremental amount matches the hand-computed value
- [ ] Integration: billing-svc killed after a transition commit → the command is still published once, and the saga completes
- [ ] Integration: duplicate `PaymentSucceeded` reply → single transition, second ignored (metric + `saga_events` show one)
- [ ] Integration: stub worker never replies → sweeper retries 3×, then `PDF_FAILED`. `RetryInvoiceSaga` resumes it
- [ ] Integration: `PaymentErrored` → `PAYMENT_UNKNOWN` → status query → `PAID` without a second charge command
- [ ] Integration: `ClosePeriod` called twice → one billing run, one invoice per customer
- [ ] Invoice numbers are gap-free under 20 concurrent finalizations (test)
- [ ] Integration: sandbox `ClosePeriodNow` mid-month → invoice `SBX-…` reaches `COMPLETED` with the stub worker within 2 min. The same call with a live key → `403`
- [ ] Unit: fractional usage `1.2345` GB-h × `"12.5"` → `15.43` (half-even at line level) in a 2-decimal currency, and `15` in `JPY`

## Dependencies

Phase 03 (usage + watermark APIs), Phase 02 (outbox watermark), Phase 01 (subscriptions, plan versions). Phase 04 not required.

## Risks / open questions

- This is the largest phase. If it runs long, move dunning to Phase 06 (keeping `PAYMENT_DECLINED → UNCOLLECTIBLE` direct).
- Zero-usage customers with only a base fee: invoice them (planned: yes, if the plan has `base_fee_minor > 0`).
- Should invoice snapshot data go in the command (planned) or should workers fetch it via gRPC? A larger message, but workers stay decoupled.
