# 22 — Platform audit and roadmap: financial correctness first

## The request
Owner, 2026-11-01: analyse the existing code (not propose features that exist), find gaps and technical risks, and produce a plan in this order: **financial correctness → payment reliability → production readiness → reconciliation → developer experience → advanced intelligence**. Vision: revenue infrastructure without vendor lock-in. Analysis and plan only; no code in this step.

## How this was made, and its limits
Two read-only passes over `main` (2026-11-01) with file evidence. **Nothing was run**: "exists" means the code and tests are present, not that they pass today. Anything not grepped to a conclusion is marked **UNVERIFIED**. The first thing the work below does is turn UNVERIFIED items into confirmed or refuted ones.

## A. Executive summary
**Maturity.** A real multi-service platform with unusual strengths for its age: payment saga with an explicit unknown-outcome state and a status query before any re-charge; idempotency keys at every provider that supports them; append-only saga/credit/payment tables by grants; forced RLS with a startup-independent verifier; gap-free invoice numbers under concurrency; late-usage and re-rating paths; chaos suite with invariants. It is not yet production-proven: nothing is verified against real provider sandboxes, and DR has no point-in-time recovery.

**Top 5 technical risks**
1. **Redelivered refunds are not guarded for iyzico and Paddle** (no ask-first as there is for charges; neither provider is idempotent in our adapter). High, partly UNVERIFIED (provider behaviour on a repeat).
2. **iyzico lost-response window**: a charge committed late can read `NotFound` and be re-charged under the same `conversationId`, which iyzico may not dedupe (UNVERIFIED). High.
3. **Financial rows are immutable by grants only**: `billing_system` can UPDATE `invoices` (totals, snapshot) and DELETE most billing tables (granted for the sandbox reset); `payment_ledger` is deletable by `payments_system`. No triggers. A bug or a wrong environment can rewrite history. High.
4. **No point-in-time recovery**: daily `pg_dump`, "you lose up to a day"; no stated RPO/RTO; unconsumed events are lost on a broker loss. High.
5. **No financial anomaly alerting and no broad audit log**: alerts cover usage correctness and infrastructure, not charge failure rate, refund spikes, invoice-vs-payment mismatch or provider webhook failures; the audit trail is narrow (`app_audit`, `go_live_audit`, saga events). High.

**Top 5 product gaps**
1. Customer-facing **event correction** (only an operator replay exists).
2. **No 2FA** for console sign-in (nothing found for TOTP/WebAuthn; account lockout exists via `users.failed_logins`/`locked_until`, and email-code and Google sign-in exist, so 2FA must wrap all methods).
3. **Reconciliation is read-only**: no mismatch workflow, no schedule (UNVERIFIED), no **settlement/payout/fee ingestion** (the `payout` ledger kind exists, nothing writes it).
4. **Upcoming-invoice / cost preview** for usage (listed missing in `docs/hosted-pages.md`).
5. **Runtime provider health, circuit breaker and failover** (the registry and per-country routing exist; health is a connect-time credential check).

**Before production:** items 1–5 of the risks; real sandbox runs for Stripe, Paddle, iyzico (plans 06–08); a restore drill that checks data, not only table existence; real alert receivers.

## B. Current state by domain
| Domain | Exists | Strengths | Gaps | Risk | Evidence |
|---|---|---|---|---|---|
| Payment saga | 19-state table, unknown-outcome state, dunning, step deadlines | Status query before re-charge; same key across re-issues; exhaustive table test | iyzico NotFound window; Stripe 24h key horizon not referenced in code (UNVERIFIED) | High | `billing-svc/internal/sagas/model/table.go`, `command/{sweep,publish}.go` |
| Provider adapters | Stripe, Paddle, iyzico, mock-psp, bank transfer | Capability registry, status normalisation, multi-account keys | Parity unverified live; Paddle refunds are `full` only; no ask-before-refund | High | `pkg/payments/*`, `docs/*-compatibility-matrix.md` |
| Webhooks in | Per-provider verification, dedupe `(provider,event_id)`, forget-on-503 | iyzico confirms by lookup instead of trusting the body | A direct duplicate-event test is UNVERIFIED | Low–Med | `payments-worker/.../webhook.go` |
| Immutability | Grants: no UPDATE on saga events, credit ledger, payment ledger, credit notes (refund cols only); DELETE is granted too, for the sandbox reset | Corrections are new rows (credit notes, re-rating) | `invoices` updatable, broad DELETE, no triggers | High | `billing 00001_init.sql`, `payments-worker 00001_init.sql` |
| Ledgers | Credit ledger (append-only), payment ledger (provider facts) | Concurrency tests for credits | Not double-entry; accounting module is an expense/income summary; `credit_ledger.idempotency_key` index not unique | Med | `reconciliation`, `accounting`, `creditnotes` |
| Reconciliation | Read-only per-period discrepancy report with 5 kinds + metric | Catches duplicate charge and mismatch | No write/resolve workflow; scheduling UNVERIFIED; no settlement | Med | `reconciliation/query/query.go` |
| Usage/metering | Ingest validation, idempotency, partitioned keys, inbox, late usage, re-rating, replay | Benchmarks published (5k/s p99 32 ms) | 20k/s p99 2.35 s unprofiled; 45-day key retention vs 35-day late window; documented hole when an on-time event takes a unit from an invoiced late event | Med | `ingest-svc`, `aggregator`, `docs/07-correctness.md`, `docs/benchmarks.md` |
| Security | Hashed API keys, permissions, Argon2id, PKCE, rate limits, RLS, AES-GCM secrets | Strong isolation design | No 2FA; single secretbox key (no rotation); verifier only in tests; log redaction opt-in; CSRF design UNVERIFIED | Med–High | `pkg/apikey`, `pkg/pgxx/rlscheck.go`, `pkg/secretbox`, `pkg/logx` |
| Reliability | DLQ with redrive, retry tiers, backups + weekly restore check | Alerts for DLQ, stuck sagas, backup | No PITR; restore check shallow; no circuit breaker/failover | High | `deploy/backup`, `docs/runbooks/backup-restore.md` |
| Observability | JSON logs, OTel metrics/traces, Grafana, 17 alerts | Trace ids through Kafka/AMQP | No error tracking; no financial anomaly alerts; no real alert receiver | High | `deploy/prometheus/rules/payrudder.yaml` |
| Developer experience | OpenAPI (116 `/v1` paths), 4 SDKs (generated), sandbox, mock PSP, webhook test+redeliver, playground, MCP | Contract test against OpenAPI | SDKs unpublished (`0.0.0`); no customer CLI; docs site has five guides and 149 operation pages; no local webhook forwarder | Med | `api/openapi`, `sdk/*`, `docs-site/` |
| Tests | ~410 Go test files, 140 integration files, chaos A1–A9, k6, mock+real e2e | Financial invariants in chaos | No live provider-sandbox CI; no fuzz; one contract test file; rolled-back-transaction test not found | Med | `tests/*` |

## C. Gap analysis
(Format per gap: current state · problem · proposed fix with least change · priority · dependencies · size.)

**G1 Refund redelivery guard (iyzico, Paddle).** Current: charge asks the provider first when it is not idempotent; refund does not. Problem: billing resends a refund up to three times after five minutes (`creditnotes/command/refund.go`) with a new command id and the same key; `payment_attempts` is keyed by command id, so a resend never looks redelivered, and `handler.go` discards the `redelivered` flag for refunds. iyzico allows repeated refunds and the Paddle adapter sends no key, so a resend can refund twice. Fix: for providers without idempotent refunds (new capability `IdempotentRefunds`), check the payment ledger and `payment_attempts` by idempotency key (not command id) before `Refund`; if the outcome is unknown never repeat it, park it for the operator; add a provider-side lookup only where the compatibility matrix documents one. (`payment_attempts` is set to DONE by `workerx` after a reply is stored; the remaining windows are a crash between the provider call and the store, transient errors, and the resend.) **Critical.** The compatibility matrices from #270 and #275 already hold the provider facts. **M.**

**Status G1 (2026-11-01): done in PR #279 (`fix/refund-guard`); fake providers only, nothing run against a real iyzico or Paddle account.** Built: capability `IdempotentRefunds` (true for Stripe, which sends the key as `Idempotency-Key` and keeps it at least 24 h; true for mock-psp and for a provider that declares nothing; false for iyzico, Paddle and bank transfer). For the others `payments-worker` (`handler.guardRefund`) looks, before `Refund`, at the payment ledger and at `payment_attempts` **by key**, not by command id: a refund the ledger or an earlier attempt shows as made is answered with its result (and a missing ledger row is written then); an earlier attempt that was only started, timed out or cannot be read is an unknown outcome, not repeated, answered `REFUND_OUTCOME_UNKNOWN`; one the provider refused may be asked again. No migration and no new state: the credit note goes to `failed` with that code in `refund_error` (the saga's void refund exhausts its steps into `REFUND_FAILED`), and `retryRefund` keeps the key after `TIMEOUT`, `REFUND_OUTCOME_UNKNOWN` and no answer (it took a new key after a timeout before, which could refund twice at any provider). The false comment in `creditnotes/command/refund.go` is fixed. A failed ledger write after a successful refund is retried twice, counted (`ledger_write_failures`), logged, and never fails the command; the stored reply answers any resend. Not built, still open: (1) a lookup at the provider: neither matrix documents one (Paddle's list adjustments by transaction exists in its API but is not in the matrix, so UNVERIFIED); (2) an operator action to resolve an unknown refund (mark it refunded, or allow a new key after checking at the provider): today the operator refunds outside Payrudder and the note stays failed; (3) a transient error (5xx, reset) on a refund to iyzico or Paddle now parks instead of retrying, since the adapters cannot say "definitely not processed" (a 429 is); (4) alerts on `ledger_write_failures` and on notes failed with `REFUND_OUTCOME_UNKNOWN` (G6); (5) whether iyzico and Paddle really refund twice on a repeat is still UNVERIFIED; the guard assumes the worst.

**G2 iyzico `NotFound` re-charge window.** Current: a not-found status after a timeout leads to a re-charge. Note: iyzico `Status` returns NotFound only if `Settings.NotFoundCodes` lists the error code, and the default is empty, so by default an unknown outcome retries and then parks; the risk exists once an owner or sandbox sets `NotFoundCodes`. Fix: add an age check in `sagas/model/table.go` and `sweep.go`: a NotFound younger than a grace period retries, one older than the provider key horizon parks; keep `NotFoundCodes` empty by default. This also applies to any provider with a retention-limited key (Stripe 24 h, UNVERIFIED): refuse to re-charge older than the key horizon. **Critical.** Needs: iyzico docs/sandbox. **M.**

**Status G2 (2026-11-01): done in PR #279 (`fix/refund-guard`); tested with stub workers and a test clock, not against a provider.** Built: a `StatusNotFound` in `PAYMENT_UNKNOWN` is read by the age of the charge under its key (the latest entry into `PAYMENT_REQUESTED` that was not a re-charge, from `saga_events`): younger than the grace period the saga stays in `PAYMENT_UNKNOWN` with no command outstanding and its step deadline set to the end of the grace period, so the sweeper's first re-issue asks the status again (`EffRecheck`); between grace and horizon the charge is sent again under the same key as before; older than the horizon the saga parks (`last_error` says why, no deadline, the operator's Retry asks again, Settle and Void still work). Settings, all **default (not agreed)**: `PAYRUDDER_NOTFOUND_GRACE` 10 minutes, `PAYRUDDER_NOTFOUND_GRACE_SANDBOX` 10 seconds (sandbox runs on compressed time like dunning), `PAYRUDDER_KEY_HORIZON` 24 hours (Stripe's retention); `NewEngine` refuses a grace that is not shorter than the horizon. iyzico `NotFoundCodes` stays empty by default. Open: iyzico's key retention and its not-found error code are UNKNOWN (matrix), so one horizon serves every provider; the `PAYMENT_PENDING` → not found → charge row is unchanged (no lost-response window there); the e2e `cus_tnot` scenario now waits for the sandbox grace (10 s) and was not run.

**G3 Database-level immutability.** Current: grants only. Fix: triggers that reject UPDATE of finalised invoice money columns and DELETE on live financial tables; the sandbox reset uses a dedicated path (a role or `SET LOCAL` flag only the reset holds), with a test that live environment rows cannot be deleted. **Critical.** Needs: map every legitimate update in `invoices.sql`/`links.sql`. **M.**

**G4 Audit log.** Current: narrow tables. Fix: one append-only `audit_events` table (actor, action, target, before/after hash, source IP, request id) written for money-affecting and security-affecting actions (keys, members, prices, refunds, credit notes, webhook endpoints, provider accounts), shown in the console and exportable; optional hash chain later. **High.** **M–L.**

**G5 PITR and DR.** Current: daily dump. Fix: WAL archiving and base backups to the target in plan 16, stated RPO/RTO, a restore drill that checks row counts and invariants (invoice totals vs lines, ledger sums), outbox replay after a broker loss. **High.** Needs: storage target (plan 16). **L.**

**G6 Financial anomaly alerts.** Fix: alerts on charge failure rate, refund spike, invoice paid without charge (already a discrepancy kind), provider webhook failure/lag, dunning growth; real receivers. **High.** Needs: plan 16 receiver decision. **S–M.**

**G7 Reconciliation as a workflow.** Current: read-only report. Fix: persist discrepancies with status (open, explained, resolved), a console review queue, schedule the run, resolution writes a ledger fact or credit note with audit; then add settlement/payout ingestion per provider (Stripe balance transactions, Paddle transactions/adjustments, iyzico settlement report — each read from current docs, UNVERIFIED until then) with expected-fee calculation and "paid ≠ settled" states. **High**, phase 3. Depends: G1–G4. **L.**

**G8 Runtime provider health and failover.** Fix: periodic health state feeding routing; circuit breaker per account; **never fail over on a timeout**. Order of operations when a provider times out: query the first provider by transaction id, then if still unknown park in reconciliation; only a definite decline or a definite `NotFound` past the key horizon may route to a second provider, and only for a new attempt with a new key. **Medium.** Depends: G1, G2, plan 10. **L.**

**G9 2FA.** TOTP and recovery codes (WebAuthn later); enforce for owners; lockout policy per account (current per-IP limits only; account lockout UNVERIFIED). **High (security).** **M.**

**G10 Secret key rotation.** Add a key id to `secretbox`, support two active keys, a re-seal job. **Medium.** **S–M.**

**G11 RLS verifier everywhere.** `VerifyForcedRLS` runs only in tests (ingest, plans, aggregator, pkg). Run it at startup or in readiness for the services that have Postgres but no verifier: assets-svc, billing-svc, payments-worker (limits-svc has no Postgres). **Medium.** **S.**

**G12 Event corrections for tenants.** A tenant-facing correction (a compensating event with a reference to the original and a reason) that flows into the same late/frozen-period rules; documented limits. **Medium–High.** Depends: ADR for how corrections interact with frozen periods. **L.**

**G13 Idempotency retention vs late window.** ADR 0014 says two to three months while `ingest-svc/internal/maintenance/maintenance.go` and `docs/07-correctness.md` say 45 days; reconcile first, then add a test at the boundary. Also add a unique partial index on `credit_ledger.idempotency_key` after a duplicate check (G19). **Medium.** **S.**

**G14 Upcoming-invoice and cost preview.** Usage × rating without closing the period, via the existing rating package; surface in the API, portal and SDKs. **Medium.** **M.**

**G15 Tests for the missing scenarios.** Refund ok at the provider but ledger write fails; DB transaction rolled back midway; direct duplicate-webhook assertion; boundary of the idempotency window; a live-sandbox nightly job (secrets-gated). **High.** Depends: plans 06–08 accounts. **M.**

**Status G15 (2026-11-01), fake-provider part: done in PR #279 (`fix/refund-guard`).** Tests that fail without the fixes: `payments-worker/internal/itest` `TestRefundResendNeverRefundsTwice` (real iyzico and Paddle adapters against an HTTP fake that does not dedupe: a resend with a new command id, a ledger write that fails after the refund, a provider that did not answer, a crash between the call and the reply, a stored timeout, a refusal that may be asked again, a new key) and `TestRefundResendReachesAnIdempotentProvider` (Stripe-like provider keeps its behaviour); `billing-svc` `TestNotFoundIsReadByTheAgeOfTheCharge` (table with both limits), `TestTableIsDeterministic` with the new guards, `TestNewEngineChecksTheNotFoundWindow`, `TestMayHaveHappened`, and the integration tests `StatusNotFoundRechargesWithTheSameIdempotencyKey` (now waits out the grace), `StatusNotFoundPastTheKeyHorizonParks` and the unknown-outcome retry inside `CreditNoteRefundsToTheCardAndIsRetriedUntilAnswered`. Still open: the live-sandbox nightly job (needs accounts), a database transaction rolled back midway, the boundary of the usage idempotency window (G13), and the chaos suite A1-A9 with a refund/charge race was not re-run.

**G16 Backoffice and DX.** Publish SDKs with versions; a customer CLI (login, tail events, forward webhooks locally); docs site: publish job and concept pages (plan 19; five guides and 149 operation pages exist); examples per language (TS first, then Go, then Python). **Medium.** **L.**

**G17 Throughput.** Profile ingest at 20k/s before any new technology; the first suspects are WAL fsync and a single hot tenant partition. Do not add systems without a measurement. **Low–Medium.** **M.**

**G18 Error tracking.** Add an error tracker (self-hostable) or structured error export; none found. **Low.** **S.**

## D. Roadmap
**Phase 0 — critical production risks.** Goal: nothing can charge or refund twice, and history cannot be rewritten. Work: G1, G2, G3, G15 (the tests that prove them). Depends: provider docs and sandbox accounts for G1/G2 (plans 06–08). Output: tests that fail without the fix; migration with triggers. Risks: triggers break the sandbox reset (design the reset path first). Success: chaos A1–A9 plus new refund/charge races green; an attempted UPDATE of a finalised invoice is rejected by the database.

**Phase 1 — production foundation.** Work: G4, G5, G6, G9, G10, G11, G13; the real alert receiver. Output: audit log, PITR with a stated RPO/RTO and a data-checking restore drill, anomaly alerts, 2FA. Success: a drill restores to a chosen minute and passes invariant checks; every money/security action appears in the audit log.

**Phase 2 — payment orchestration.** Work: G8, plan 10 (integrations page), plans 06–08. Success: timeout → query → reconciliation path proven by chaos with a fault-injecting provider; failover only on definite failures.

**Phase 3 — reconciliation.** Work: G7. Success: every charge in a period is matched to a provider transaction and a settlement, or is an open item with an owner; "paid" and "settled" are different states in the console and API.

**Phase 4 — developer experience.** Work: G16, G14, G12, plans 18–20. Success: a developer integrates in under 30 minutes from the docs, TypeScript first, then Go, then Python.

**Phase 5 — advanced revenue infrastructure.** Usage intelligence, cost attribution, profitability and pricing intelligence — only after phases 0–3, and not started by this plan. Not scoped.

## E. Final priority list
| # | Work | Why now | Size | Depends |
|---|---|---|---|---|
| 1 | G1 refund redelivery guard | Real-money double refund | M | provider docs |
| 2 | G2 re-charge window / key horizon | Real-money double charge | M | iyzico docs |
| 3 | G3 DB immutability | History can be rewritten | M | map of legitimate updates |
| 4 | G15 missing scenario tests | Proves 1–3 | M | sandbox accounts |
| 5 | G4 audit log | Accountability for every later fix | M–L | – |
| 6 | G6 anomaly alerts + receiver | Detect before customers do | S–M | plan 16 |
| 7 | G5 PITR + data-checking drill | A day of loss is not acceptable | L | storage target |
| 8 | G9 2FA | Console controls money | M | – |
| 9 | G11 RLS verifier at startup, G10 key rotation, G13 window test | Cheap safety | S | – |
| 10 | G8 health, breaker, safe failover | Provider outage resilience | L | 1, 2, plan 10 |
| 11 | G7 reconciliation workflow, settlement | Paid ≠ settled | L | 3, 4 |
| 12 | G12 event corrections, G14 cost preview | Product gaps | L / M | ADR |
| 13 | G16 SDK publishing, CLI, docs | Adoption | L | plan 19 |
| 14 | G17, G18 | Measure before building | M / S | – |

## Order against the other plans
Phase 0 and 1 outrank plans 09–12, 14, 15, 17–21 (they are about whether money is safe). Plans 06–08 (provider parity and sandboxes) are prerequisites for G1, G2, G15 and stay first among the provider work. Plan 16 (backup/alerting) is absorbed by G5/G6. Plan 21 (contracts) is independent and may run in parallel because it does not touch the payment path except the new acceptance gate.

## Decisions needed (owner)
Reaching RPO/RTO targets and the backup storage target; alert receiver; whether Paddle partial refunds are in scope; whether tenants may correct events and how that interacts with frozen periods; which providers' settlement reports to ingest first.

## Not verifiable here
Everything about real provider behaviour (repeat refunds, late-committed charges, key retention), restore time, throughput beyond the published benchmarks.

## Review addendum (2026-11-01)
- **Corrections from review:** (a) legitimate invoice UPDATEs are `SetInvoiceTotals` (draft creation), `SetInvoiceStatus` (sagas/transition), `OpenInvoice` (number, status, finalized_at), `SetInvoicePDFKeyBySaga`, `SetInvoicePromotion` and `SetInvoicePaymentMethod` (snapshot.payment_method only): the G3 trigger must allow exactly those and reject money-column changes on a non-draft invoice; the only DELETE is the sandbox reset (`reset.sql`). Precedents for triggers: `config_promotions_one_way`, `go_live_audit_live_only`. (b) The `payment_discrepancies` counter has no alert using it; `ReconcileDriftHigh` is a limits-svc counter. (c) A duplicate-webhook test exists (`payments-worker/internal/itest/accounts_integration_test.go`). (d) G4 owner: one `audit_events` table per service database with a shared `pkg/audit`; the console fans out reads (default, not agreed). Plans 12-15 and 21 write to it.
- **Known money-logic findings outside G1-G18** (from `docs/phases/ROADMAP.md` and `docs/code-quality/billing-svc-status.md`, to be re-verified before work): tax of credit notes not reversed, negative total clamped to zero, trial charged the whole fee, UTC year in invoice numbers.
- **G15 is split:** "fake-provider tests now" (this plan) and "live sandbox later" (needs accounts).
- **Order:** phases 0 and 1 run before plans 09-12, 14, 15 and 17-21 and in parallel with 02-05 (different files); 13's remaining work and 16 are part of phases 1.
- **Defaults (not agreed):** RPO 15 min, RTO 1 h; alert receiver webhook/Mailpit locally; the 2FA method is TOTP with recovery codes first.
