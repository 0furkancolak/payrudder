# Phase 20 — Plan changes: upgrade, downgrade, interval switch

## Goal

A customer can move between plans and between monthly and yearly with a correct, previewed, auditable result. See [15-billing-lifecycle.md](../15-billing-lifecycle.md) and [ADR 0017](../adr/0017-advance-fixed-fees-and-change-modes.md).

## Scope

**In:** ChangeSubscription and PreviewChange (one code path), timing and proration modes with tenant defaults, scheduled changes, quantity, credit balance applied at invoicing, mapping onto Paddle's modes, console dialog with live preview.

**Out of scope:** credit notes and refunds to card (phase 22), Paddle catalogue creation (phase 21).

## Tasks

- [ ] Domain: change calculator and tests for every mode and direction
- [ ] plans-svc: subscription_changes, scheduling, cancel of a scheduled change
- [ ] billing-svc: proration lines, credit balance, application of due changes in the period-start run
- [ ] Gateway, OpenAPI, console dialog
- [ ] Paddle mapping with fixtures
