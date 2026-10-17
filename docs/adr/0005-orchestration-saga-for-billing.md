# ADR 0005 — Orchestration-based saga for invoicing

- Status: Accepted
- Date: 2026-10-17

## Context

Producing and collecting an invoice spans billing state, PDF storage, a payment provider and email. There's no
distributed transaction across them. Steps can fail, time out or be delivered twice. Finance needs to see *where*
an invoice is and *why* it stopped.

## Decision

- billing-svc is the **orchestrator**. Each invoice has an `invoice_sagas` row holding an explicit state machine
  ([billing-svc.md](../03-services/billing-svc.md#saga-state-machine)).
- Steps are classified as compensatable (draft, finalize, PDF), **pivot** (payment charge) and retriable (notification).
- Each transition commits atomically with its outgoing command (outbox) and an audit row (`saga_events`).
- Transitions are guarded by optimistic concurrency (`version`) and the expected state. Commands have deterministic IDs.
- Every waiting state has a deadline. A sweeper retries and then moves to an operator-visible `*_FAILED` state.
- Ambiguous payment outcomes go to `PAYMENT_UNKNOWN` and are resolved by querying the PSP, never by re-charging.

## Alternatives considered

| Option | Why not |
|---|---|
| Choreography (services react to each other's events) | No single place holds invoice state, so the flow is implicit across services and harder to debug or show to finance. Compensation logic gets scattered |
| Temporal / workflow engine | Excellent fit, but hides the mechanics this project wants to demonstrate and adds a large dependency |
| Synchronous calls in one request | Long-running (PDF, PSP latency, dunning over days). Ties up a request and loses progress on crash |
| 2PC / XA | Not supported by the PSP, SMTP or object store, and blocking anyway |

## Consequences

- One table answers "what is the state of invoice X and how did it get there".
- billing-svc is coupled to the worker contracts (`commands.v1`), but that's explicit and versioned.
- The orchestrator must be crash-safe and idempotent. The outbox plus guarded transitions make it so.
- More code than choreography for a small flow, but testable as a pure transition table.
