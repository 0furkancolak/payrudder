# Phase 44 — Correctness fixes from the code-quality report

## Source

[docs/code-quality/README.md](../code-quality/README.md), section "Önce düzeltilmesi gerekenler" (21 items, money first). Each item has a scenario in its detail file; **write the failing test from that scenario first**, then fix.

## Three PRs, disjoint files, can run in parallel

### 44a — Money path (billing-svc, plans-svc)

| Item | Where |
|---|---|
| Dunning charges the whole invoice after a partial bank transfer; charge `amount_due` | `billing-svc/internal/saga/engine.go:446` |
| Void refund ignores credit notes; void can issue a new refund credit note | `engine.go:456` |
| Credits applied to a voided invoice are lost; reopen the lots | saga void path |
| Proration credit written as `KindCredit` is not taken off the tax base; excess VAT | `runs/` tax step |
| Re-rating re-bills trial, advance-fee and provider-owned subscriptions | `rerating` |
| Provider migration successor always arrears; a yearly advance subscription gets a free year | `providermigrations/command/command.go:60-70` |
| Plan change ignores the coupon when computing the credit | `changes/query/query.go:128-138` |
| iyzico confirmation trusts the unsigned conversation id, empty status = success, mock 404 = success | `pkg/psp` iyzico, mockpsp |

### 44b — Data correctness (aggregator, ingest, gateway, web)

| Item | Where |
|---|---|
| Replay promote deletes periods outside the replay range and the report hides it | `aggregator/.../replay.go:620-636` |
| Ingest dedup window (~28 d) shorter than allowed lateness (35 d) | ingest idempotency retention, ADR 0014 |
| Console money uses `Intl` fraction digits; 16 currencies are 100–1000× wrong | `web/admin/src/shared/lib/money.ts:42`: take exponents from the backend currency table |
| Hosted invoice page stuck on "processing" after an abandoned checkout or opened bank instructions | gateway hosted page |
| `ledger_fact_once` unique index lacks `tenant_id` | migration + sqlc |

### 44c — Concurrency and resilience (pkg, workers, plans-svc identity)

| Item | Where |
|---|---|
| `workerx` and invoice-worker lock maps race | `pkg/workerx`, invoice-worker |
| `rabbitx` does not wait for in-flight handlers on shutdown; immediate requeue floods the DLQ | `pkg/rabbitx` |
| `uuid.MustParse` panics on a bad message | `payments-worker/internal/einvoice/handler.go:169` |
| Password lockout bypassed by parallel requests | `plans-svc/internal/identity` (count in the same statement, as the OTP does) |

## Acceptance

Every item has a test that failed before and passes after; `docs/error-keys.md` regenerated; no behavior change outside the item.
