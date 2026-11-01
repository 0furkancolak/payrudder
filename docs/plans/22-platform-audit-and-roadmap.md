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
2. **No 2FA** for console sign-in (nothing found for TOTP/WebAuthn).
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
| Immutability | Grants: INSERT-only on saga events, credit ledger, payment ledger, credit notes (refund cols only) | Corrections are new rows (credit notes, re-rating) | `invoices` updatable, broad DELETE, no triggers | High | `billing 00001_init.sql`, `payments-worker 00001_init.sql` |
| Ledgers | Credit ledger (append-only), payment ledger (provider facts) | Concurrency tests for credits | Not double-entry; accounting module is an expense/income summary; `credit_ledger.idempotency_key` index not unique | Med | `reconciliation`, `accounting`, `creditnotes` |
| Reconciliation | Read-only per-period discrepancy report with 5 kinds + metric | Catches duplicate charge and mismatch | No write/resolve workflow; scheduling UNVERIFIED; no settlement | Med | `reconciliation/query/query.go` |
| Usage/metering | Ingest validation, idempotency, partitioned keys, inbox, late usage, re-rating, replay | Benchmarks published (5k/s p99 32 ms) | 20k/s p99 2.35 s unprofiled; 45-day key retention vs 35-day late window; documented hole when an on-time event takes a unit from an invoiced late event | Med | `ingest-svc`, `aggregator`, `docs/07-correctness.md`, `docs/benchmarks.md` |
| Security | Hashed API keys, permissions, Argon2id, PKCE, rate limits, RLS, AES-GCM secrets | Strong isolation design | No 2FA; single secretbox key (no rotation); verifier only in tests; log redaction opt-in; CSRF design UNVERIFIED | Med–High | `pkg/apikey`, `pkg/pgxx/rlscheck.go`, `pkg/secretbox`, `pkg/logx` |
| Reliability | DLQ with redrive, retry tiers, backups + weekly restore check | Alerts for DLQ, stuck sagas, backup | No PITR; restore check shallow; no circuit breaker/failover | High | `deploy/backup`, `docs/runbooks/backup-restore.md` |
| Observability | JSON logs, OTel metrics/traces, Grafana, 17 alerts | Trace ids through Kafka/AMQP | No error tracking; no financial anomaly alerts; no real alert receiver | High | `deploy/prometheus/rules/payrudder.yaml` |
| Developer experience | OpenAPI (116 `/v1` paths), 4 SDKs (generated), sandbox, mock PSP, webhook test+redeliver, playground, MCP | Contract test against OpenAPI | SDKs unpublished (`0.0.0`); no customer CLI; docs site has one page; no local webhook forwarder | Med | `api/openapi`, `sdk/*`, `docs-site/` |
| Tests | ~410 Go test files, 140 integration files, chaos A1–A9, k6, mock+real e2e | Financial invariants in chaos | No live provider-sandbox CI; no fuzz; one contract test file; rolled-back-transaction test not found | Med | `tests/*` |

## C. Gap analysis
(Format per gap: current state · problem · proposed fix with least change · priority · dependencies · size.)

**G1 Refund redelivery guard (iyzico, Paddle).** Current: charge asks the provider first when it is not idempotent; refund does not. Problem: a redelivered refund command can refund twice. Fix: for non-idempotent providers, look up the existing refund (iyzico/Paddle API) before sending; record the refund reply in `payment_attempts` (nothing sets `DONE` today, UNVERIFIED reading). **Critical.** Needs: provider docs per plans 06/07. **M.**

**G2 iyzico `NotFound` re-charge window.** Current: a not-found status after a timeout leads to a re-charge. Fix: after a timeout, do not re-charge until a grace period past iyzico's own processing time has passed, then re-query; if still unknown, park for reconciliation, never auto-re-charge. This also applies to any provider with a retention-limited key (Stripe 24 h, UNVERIFIED): refuse to re-charge older than the key horizon. **Critical.** Needs: iyzico docs/sandbox. **M.**

**G3 Database-level immutability.** Current: grants only. Fix: triggers that reject UPDATE of finalised invoice money columns and DELETE on live financial tables; the sandbox reset uses a dedicated path (a role or `SET LOCAL` flag only the reset holds), with a test that live environment rows cannot be deleted. **Critical.** Needs: map every legitimate update in `invoices.sql`/`links.sql`. **M.**

**G4 Audit log.** Current: narrow tables. Fix: one append-only `audit_events` table (actor, action, target, before/after hash, source IP, request id) written for money-affecting and security-affecting actions (keys, members, prices, refunds, credit notes, webhook endpoints, provider accounts), shown in the console and exportable; optional hash chain later. **High.** **M–L.**

**G5 PITR and DR.** Current: daily dump. Fix: WAL archiving and base backups to the target in plan 16, stated RPO/RTO, a restore drill that checks row counts and invariants (invoice totals vs lines, ledger sums), outbox replay after a broker loss. **High.** Needs: storage target (plan 16). **L.**

**G6 Financial anomaly alerts.** Fix: alerts on charge failure rate, refund spike, invoice paid without charge (already a discrepancy kind), provider webhook failure/lag, dunning growth; real receivers. **High.** Needs: plan 16 receiver decision. **S–M.**

**G7 Reconciliation as a workflow.** Current: read-only report. Fix: persist discrepancies with status (open, explained, resolved), a console review queue, schedule the run, resolution writes a ledger fact or credit note with audit; then add settlement/payout ingestion per provider (Stripe balance transactions, Paddle transactions/adjustments, iyzico settlement report — each read from current docs, UNVERIFIED until then) with expected-fee calculation and "paid ≠ settled" states. **High**, phase 3. Depends: G1–G4. **L.**

**G8 Runtime provider health and failover.** Fix: periodic health state feeding routing; circuit breaker per account; **never fail over on a timeout**. Order of operations when a provider times out: query the first provider by transaction id, then if still unknown park in reconciliation; only a definite decline or a definite `NotFound` past the key horizon may route to a second provider, and only for a new attempt with a new key. **Medium.** Depends: G1, G2, plan 10. **L.**

**G9 2FA.** TOTP and recovery codes (WebAuthn later); enforce for owners; lockout policy per account (current per-IP limits only; account lockout UNVERIFIED). **High (security).** **M.**

**G10 Secret key rotation.** Add a key id to `secretbox`, support two active keys, a re-seal job. **Medium.** **S–M.**

**G11 RLS verifier everywhere.** Run `VerifyForcedRLS` at startup (or in a readiness check) for every service and add the missing services (billing, payments-worker, limits) to the tests. **Medium.** **S.**

**G12 Event corrections for tenants.** A tenant-facing correction (a compensating event with a reference to the original and a reason) that flows into the same late/frozen-period rules; documented limits. **Medium–High.** Depends: ADR for how corrections interact with frozen periods. **L.**

**G13 Idempotency retention vs late window.** The 35-day late window against 45-day key retention leaves 10 days of margin; confirm with ADR 0014 and the benchmark, and add a test at the boundary. **Medium.** **S.**

**G14 Upcoming-invoice and cost preview.** Usage × rating without closing the period, via the existing rating package; surface in the API, portal and SDKs. **Medium.** **M.**

**G15 Tests for the missing scenarios.** Refund ok at the provider but ledger write fails; DB transaction rolled back midway; direct duplicate-webhook assertion; boundary of the idempotency window; a live-sandbox nightly job (secrets-gated). **High.** Depends: plans 06–08 accounts. **M.**

**G16 Backoffice and DX.** Publish SDKs with versions; a customer CLI (login, tail events, forward webhooks locally); docs site beyond one page (plan 19); examples per language (TS first, then Go, then Python). **Medium.** **L.**

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
