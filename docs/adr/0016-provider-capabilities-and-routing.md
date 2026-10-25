# ADR 0016: Payment providers declare capabilities; routing is a stored rule set

Status: accepted for point 4 (built in phase 13); the rest is proposed.

## Context

Payrudder has one `PaymentProvider` interface and a mock. Real providers differ in what they own: iyzico leaves recurring schedules, dunning and invoice documents to the merchant;
Paddle, as merchant of record, owns renewal, dunning and tax. A lowest-common-denominator interface would either hide Paddle's strengths or make Payrudder do work twice.

## Decision

1. Each adapter exposes `Capabilities()`. Every subscription has a single `owner` for renewal and dunning, taken from the provider's capabilities at creation.
2. Provider selection is data: ordered `routing_rule` rows (country, currency to provider), evaluated once at subscription creation and stored on the subscription.
3. A provider migration is an explicit saga that cancels the old subscription only after the new provider's first charge is confirmed.
4. Currency is fixed per subscription. The tenant keeps a default currency. This supersedes the single-currency-per-tenant rule in ADR 0010 and `docs/06-multi-tenancy.md`
   for subscriptions; money stays minor units.
5. Provider events enter through verified webhooks into an inbox and are processed idempotently, backed by a periodic reconciliation job.

## Consequences

- The saga branches on capabilities; tests cover each owner mode.
- Routing changes never move existing customers, so a rule edit is safe.
- Reports cannot sum across currencies without a tenant-supplied rate.
- Provider behaviour in the capability table is an assumption until confirmed in each provider's sandbox.
