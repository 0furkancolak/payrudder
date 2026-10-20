# Phase 19 — Yearly intervals and fixed fees in advance

## Goal

Subscriptions can be monthly or yearly and the fixed fee is invoiced at the start of each period. See [15-billing-lifecycle.md](../15-billing-lifecycle.md) and [ADR 0017](../adr/0017-advance-fixed-fees-and-change-modes.md).

## Scope

**In:** interval and anchor on new subscriptions (anniversary), plan version fees per interval and currency, a period-start fee run in billing-svc, trial/pause/cancel behaviour for advance fees, MRR normalisation of yearly fees, proto/gateway/OpenAPI, docs.

**Out of scope:** plan changes (phase 20), Paddle prices (phase 21), credit notes (phase 22).

## Tasks

- [ ] pkg/subscription: anniversary periods, period-start boundaries
- [ ] plans-svc: yearly accepted, fees per interval, migrations
- [ ] billing-svc: period-start fee run, idempotent per subscription and period; usage run unchanged
- [ ] Reports: MRR with yearly normalised
- [ ] Tests: unit, integration (yearly, leap day, trial, pause, cancel)
