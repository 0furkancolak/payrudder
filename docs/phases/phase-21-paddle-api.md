# Phase 21 — Paddle catalogue and full subscription API

## Goal

Quotify creates and keeps in sync what Paddle needs and exposes every Paddle subscription operation. See [15-billing-lifecycle.md](../15-billing-lifecycle.md) and [ADR 0017](../adr/0017-advance-fixed-fees-and-change-modes.md).

## Scope

**In:** Catalogue interface; products, prices, customers, addresses, businesses, discounts; subscription update, pause, resume, cancel, preview, scheduled change; partial refunds and credit adjustments; portal session; update-payment-method link; extended webhooks; retry and typed errors; console sync status.

**Out of scope:** Paddle features outside the list in the design, a Paddle sandbox run (needs credentials).

## Tasks

- [ ] pkg/payments: Catalogue and related optional interfaces
- [ ] paddle adapter: each call with recorded fixtures
- [ ] payments-worker: sync on publish, event handling
- [ ] Gateway, OpenAPI, console
- [ ] Sandbox run (needs credentials)
