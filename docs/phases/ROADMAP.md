# Roadmap

Everything built is described in the design documents of `docs/` and in the ADRs; the phase files of finished work are gone (`git log -- docs/phases` has them). This page lists only what is **left**.

## Open phases

| Phase | Goal | Done when |
|---|---|---|
| [28 Cells](phase-28-cells.md) | Tenant directory, schema-per-tenant mode, converted cross-tenant jobs, fenced moves ([ADR 0018](../adr/0018-tenant-isolation-modes.md)) | Suites pass in pooled and cells mode; a tenant in a cell cannot read another's schema; a move loses no row |
| [43 Repair CI](phase-43-ci-repair.md) | `main` and the nightly runs green. The 1M-event correctness and k6 nightlies must pass twice in a row | `ci` green on `main`, nightly green twice |
| [44 Correctness fixes](phase-44-correctness-fixes.md) | The 21 money, data and concurrency defects of the [code-quality report](../code-quality/README.md), each with a failing test first | Every item has a test that failed before and passes after |
| [49 Code-quality remediation](phase-49-code-quality-remediation.md) | Error contract, shared packages, ADR 0020 module moves (plans-svc and gateway are done; the other services are not) | Behaviour unchanged, tests green |

43, 44 and 49 were written before later work landed on `main`: check each against the code and cut what is already done before starting.

## Built, never run against the real thing

These need an account or an environment the repository does not have. Each is a run to make and record, not code to write.

- **iyzico** sandbox (phase 14); **Paddle** sandbox, including the catalogue and every subscription call (15, 21); a **provider migration** against both sandboxes (16); a yearly subscription and a plan change against a sandbox (19, 20).
- **Bank transfer** against a real bank (29); **e-invoicing** through the official UBL-TR / Peppol schematron and Nilvera's test portal (30); **credit-pack checkouts** at the providers (36); **Google sign-in** against Google (42).
- **Receipt scanning** (54): Tesseract.js OCR of a photographed receipt in a real browser against the jsdelivr CDN, and the **inbound invoice webhook** (`POST /inbound/invoices`) against a real mail provider (Resend or Nafru): both are covered only by mocks and fixtures.
- **Payrudder as a SaaS** (55): the path gateway → ingest → limits → billing for the platform tenant's own metering and plan changes is tested against fakes and plans-svc integration only, never end to end on a stack; **Nafru Email** (53): the connection check and live sending against a real Nafru deployment. The Paddle mark has no licence entry in Simple Icons: confirm Paddle's brand guidelines before a public release.
- **Railway**: a real deploy of the config in `deploy/railway` (31).
- **Kubernetes**: a fresh 3-node kind install reaching ready in under 10 minutes, and the 100k chaos run with random pod deletion on a multi-node cluster (10).

## Observability checks still manual

- Grafana "explore trace from log" (derived fields on `trace_id`) and a README screenshot of the end-to-end trace.
- Killing the aggregator for 60 s fires `ConsumerLagHigh` (documented manual check).

## Known gaps in the console

- Quota windows: hour, day, ISO week and billing month only; yearly and lifetime windows and unique/last aggregations are not built. Short windows are not checked against the aggregator.
- The sandbox provider connection has no backend.
- A dialog opened from a row of a token-paged list (reverse a commission, mark a payout paid, a partner's forms) comes back from the address only when its row is on the first page after a reload; the sheet of the row itself is looked for on later pages. A sheet that holds typed values or a one-time secret (the new key's plaintext, a scanned receipt) comes back empty.
- Apps cannot be renamed or deleted (no API); credit for quantity changes and tax calculation are not built.
- A trial's end is shown and reminded in the console only: there is no `subscription.trial_will_end` webhook or e-mail yet (it needs a job over all tenants that emits the event once per trial).
- The first-run wizard's progress and the tours that were seen are kept per person in the browser, not on the server, so a second browser starts them again (the wizard only for somebody who still has no app).

## Definition of done (every phase)

- Acceptance criteria in the phase file are met and covered by automated tests where marked.
- `make lint test test-integration` green locally and in CI.
- Docs updated: service spec, messaging, data model, and any new ADR. A finished phase file is deleted in its closing PR and its lasting decisions live in those documents.
