# Phase 22 — Billing documents

## Goal

Invoices, credit notes and coupons that a finance team accepts. See [15-billing-lifecycle.md](../15-billing-lifecycle.md) and [ADR 0017](../adr/0017-advance-fixed-fees-and-change-modes.md).

## Scope

**In:** gapless invoice numbers, typed line items with service periods, credit notes, customer credit balance reports, coupons, tax lines as given, invoice PDF and hosted page, outbound events, dunning settings, console pages.

**Out of scope:** tax calculation, revenue recognition.

## Tasks

- [ ] billing-svc: numbering, lines, credit notes, balance
- [ ] coupons (plans-svc) and their application
- [ ] PDF worker template, events
- [ ] Reconciliation accepts credit notes
- [ ] Console pages, six languages, mocked-gateway Playwright
