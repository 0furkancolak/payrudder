# Roadmap

Everything built is described in the design documents of `docs/` and in the ADRs; the phase files of finished work are gone (`git log -- docs/phases` has them). This page lists only what is **left**.

## Open phases

| Phase | Goal | Done when |
|---|---|---|
| [28 Cells](phase-28-cells.md) | **Stage 3 is left**: the directory, the isolation switch and moves are built and ingest-svc and assets-svc run in cells mode, but 30 of 41 cross-tenant jobs and tenant schemas (billing-svc, plans-svc, aggregator, payments-worker) are not converted, so the directory refuses every cell placement ([ADR 0018](../adr/0018-tenant-isolation-modes.md)) | Every entry of `pkg/tenantdir.Jobs` converted and tested in both modes |
| [43 Repair CI](phase-43-ci-repair.md) | `main` and the nightly runs green. The 1M-event correctness and k6 nightlies must pass twice in a row | `ci` green on `main`, nightly green twice |
| [49 Code-quality remediation](phase-49-code-quality-remediation.md) | Error contract, shared packages, ADR 0020 module moves (plans-svc and gateway are done; the other services are not) | Behaviour unchanged, tests green |

43 and 49 were written before later work landed on `main`: check each against the code and cut what is already done before starting.

## Built, never run against the real thing

These need an account or an environment the repository does not have. Each is a run to make and record, not code to write.

- **iyzico** sandbox (phase 14); **Paddle** sandbox, including the catalogue and every subscription call (15, 21); a **provider migration** against both sandboxes (16); a yearly subscription and a plan change against a sandbox (19, 20).
- **Bank transfer** against a real bank (29); **e-invoicing** through the official UBL-TR / Peppol schematron and Nilvera's test portal (30); **credit-pack checkouts** at the providers (36); **Google sign-in** against Google (42).
- **Receipt scanning** (54): Tesseract.js OCR of a photographed receipt in a real browser against the jsdelivr CDN, and the **inbound invoice webhook** (`POST /inbound/invoices`) against a real mail provider (Resend or Nafru): both are covered only by mocks and fixtures.
- **Payrudder as a SaaS** (55): the path gateway → ingest → limits → billing for the platform tenant's own metering and plan changes is tested against fakes and plans-svc integration only, never end to end on a stack; **Nafru Email** (53): the connection check and live sending against a real Nafru deployment. The Paddle mark has no licence entry in Simple Icons: confirm Paddle's brand guidelines before a public release.
- **Quota windows** (yearly, lifetime, and the hour totals that reconcile hour, day and week counters): the aggregator's window reads and hour totals are tested against Postgres, and limits-svc's window counters against Redis with a fake aggregator, but the two never ran together over Kafka (the `TestAggregator` and `TestLimits` Kafka suites, including a replay with hour totals and a Redis flush with a lifetime counter).
- **Railway**: a real deploy of the config in `deploy/railway` (31).
- **Kubernetes**: a fresh 3-node kind install reaching ready in under 10 minutes, and the 100k chaos run with random pod deletion on a multi-node cluster (10).

## Observability checks still manual

- Grafana "explore trace from log" (derived fields on `trace_id`) and a README screenshot of the end-to-end trace.
- Killing the aggregator for 60 s fires `ConsumerLagHigh` (documented manual check).

## Known gaps in the console

- Quotas cannot count distinct values (`UNIQUE`) or the latest value (`LAST`): the counters, the aggregator's upserts, late usage and rating only add or keep a maximum, so these two aggregations need their own storage and billing rules ([limits-svc.md](../03-services/limits-svc.md#as-built-quota-windows-and-modes)).
- The sandbox provider connection has no backend.
- A dialog opened from a row of a token-paged list (reverse a commission, mark a payout paid, a partner's forms) comes back from the address only when its row is on the first page after a reload; the sheet of the row itself is looked for on later pages. A sheet that holds typed values or a one-time secret (the new key's plaintext, a scanned receipt) comes back empty.
- Seats (a quantity on the subscription) exist in the API only: the console neither shows nor sets them. Tax is the tenant's configured rate per customer country; a real tax engine (place of supply, registrations, reverse charge, rates by date) is not built and not planned.

## Definition of done (every phase)

- Acceptance criteria in the phase file are met and covered by automated tests where marked.
- `make lint test test-integration` green locally and in CI.
- Docs updated: service spec, messaging, data model, and any new ADR. A finished phase file is deleted in its closing PR and its lasting decisions live in those documents.
