# ADR 0027 — Corrections to usage events

- Status: Accepted as the implementer's default (not agreed with the owner; plan 22 G12 listed it as a decision)
- Date: 2026-11-02

## Context

An accepted event is a fact: `202` means it is counted exactly once and billed exactly once ([07-correctness](../07-correctness.md)). A tenant that sent the wrong quantity (a
unit mistake, a duplicate under two keys, an import run twice) had no way to say so: sending the right event again adds to the wrong one, and the only repair was an
operator re-rating or a replay, neither of which is the tenant's. Changing an accepted event in place would break what the rest of the pipeline rests on: the aggregates, the
Kafka log, the limits counters and the finalized invoices are all built from immutable events, and "the value of period P" must not depend on when you ask.

## Decision

**A correction is a new event, never an edit.** It is sent to `POST /v1/events/corrections` (a batch of up to 500, answered per correction like `POST /v1/events`), with the same
key rules as an event and its own request shape, so that `POST /v1/events` and its typed `event_time` do not change for anyone:

```json
{ "corrections": [ { "idempotency_key": "fix-7", "corrects": "evt-123", "reason": "imported twice", "customer_external_id": "cus_1", "meter_code": "api_calls", "quantity": "80" } ] }
```

- `corrects` is the idempotency key of the event to correct (the original, as the tenant sent it). `reason` is required, 1 to 500 characters. `customer_external_id` and
  `meter_code` are required and must be the original's (a guard against correcting the wrong thing). A correction has no `event_time` and no `properties`: it lands at the
  original's time, which a person fixing a quantity in the console does not know to the microsecond (ingest-svc still refuses either if a caller of its gRPC API sets them).
- **`quantity` is the quantity the original event should have had.** `0` cancels it. The service works out the difference between that and what the event stands at now (its
  own quantity plus every earlier correction of it) and publishes that difference as the event's signed `quantity_units`. A replacement and not a delta, because "make it 80"
  means the same whenever it is read and survives a second correction of the same event; a delta would have the tenant doing arithmetic on a number it no longer knows.
  Corrections of one original are serialised by a transaction-scoped advisory lock on (tenant, original event id) while the difference is read and written, so two
  concurrent corrections each see the other's.
- **The event keeps the original's time.** Its `event_time` and `period_id` are the original's, so the adjustment lands in the original's period; the new field
  `UsageEvent.correction` (`corrects_event_id`, `reason`, `corrected_by`) marks it, and `quantity_units` may be negative for such an event and for no other
  (`pkg/usage.Decode`).
- **It follows the rules every event follows; nothing special is added to the aggregator.** Classification is by `ingested_at`, as always:
  - the original's period is still open, or closed but inside its grace period (`ingested_at < cutoff`): the difference is added to `usage_aggregates` of that period (and to
    the hour totals of the original's hour), exactly, and the invoice of the period carries it;
  - the period is past its cutoff, or frozen by a billing run: the difference is **late usage** of the original's period, billed on the invoice of the period the correction
    was ingested in, as a `LATE_USAGE` line priced incrementally, `rate(on-time + prior + late) − rate(on-time + prior)` under the original period's plan version, so tiers are
    honoured. A cancelled or reduced event makes a **negative** line. It is billed once: it is one `late_usage` row, summed with the other late events of the same period, and
    the lines earlier invoices carry are subtracted by `PriorLateUsage` as for any late usage. Finalized invoices never change.
  - the key of the original is looked up in the dedup window ([ADR 0014](0014-idempotency-key-retention.md)): a key is remembered for at least 45 and less than 76 days, and
    **a correction is possible exactly as long as the key is**. After that the original is unknown and the correction is refused (`CORRECTION_UNKNOWN_EVENT`). No other limit.
- **A correction is idempotent by its own key**, with the same dedup and payload-hash rules as an event (the hash covers `corrects`, `reason`, customer, meter and quantity), so a
  retry is `DUPLICATE` and adds nothing. A request that is refused claims no key. A correction that changes nothing (the event already stands at that quantity) is accepted and
  publishes a difference of zero: it leaves the audit trail and nothing else.
- **What can be corrected.** Meters that **add**: `SUM`, and `COUNT` (an event counts one, whatever its quantity, so the only correction is to cancel it, quantity `0`, or to
  count it again after it was cancelled, any positive quantity), and without a property filter (the filter judged the original's properties, which are not kept, so ingest
  could not say whether the original was counted). A correction of an event of a `MAX`, `LAST` or `UNIQUE` meter, or of a filtered meter, is refused with
  `CORRECTION_UNSUPPORTED_METER`: a maximum, a latest value and a set of distinct values have no signed difference ([ADR 0025](0025-unique-and-last-aggregations.md)), and a late
  `LAST` event is not billed at all by design. A correction of a correction is refused (`CORRECTION_OF_CORRECTION`): correct the original again, with the quantity it should have.
  The aggregator and limits-svc ignore a correction of such a meter that reaches them anyway (counted in `aggregator_unsupported_corrections`), so the table above cannot be bypassed
  by a meter that changed after the event.
- **Audit.** Each correction is a row of `event_corrections` in ingest-svc's database (the correcting event, the original event, the difference, the reason and who: the
  signed-in person's address, or `key:<api key id>`), kept for good; the event on the topic carries the same. No webhook: the tenant made the call and gets the answer in it.

## What does not change

- The finalized invoice of a closed period is never edited; a correction after it is a line on a later invoice, like late usage always was.
- Late `LAST` and `UNIQUE` behaviour, including the known hole of [ADR 0025](0025-unique-and-last-aggregations.md) (an on-time event processed after an already invoiced late
  event of the same value moves the unit back without amending the invoice), is untouched, because corrections of those meters are refused. A test pins both: the correction is
  refused and the aggregates do not move.
- Replays re-aggregate from the log; a correction is an event in it, so a rebuilt generation includes it. (A replay classifies by `ingested_at` alone, as for every event.)

## Known limits (default, not agreed)

- **A downward correction larger than the next invoice** charges nothing, and the surplus is carried to the customer's credit balance ([ADR 0028](0028-money-rules-credit-note-tax-surplus-trial-year.md)
  section 2; it was clamped and lost before): the invoice's total is zero, its negative `LATE_USAGE` line keeps its amount, and what the lines come to below zero (tax included)
  is one credit, applied to the customer's next invoices.
- **Events older than the migration cannot be corrected**: their keys were stored without the customer, meter and time a correction needs. This lasts for the 76 days of the key
  window after the release.
- **The estimate of the upcoming invoice** ([billing-svc](../03-services/billing-svc.md#as-built-the-upcoming-invoice-plan-22-g14)) includes a pending correction like any late usage.
- A correction is a signed event in analytics-svc's raw table and in limits-svc's counters (a counter moves down by the difference); analytics reports totals of raw quantities,
  so they follow.

## Alternatives considered

| Option | Why not |
|---|---|
| Edit or delete the original | Breaks immutability of the log, the aggregates' replayability and every consumer's dedup; "the value of P" would depend on when you ask |
| The tenant sends a signed delta | Arithmetic on a number the tenant no longer knows; two corrections of one event compose in arrival order; no way to say "cancel it" without knowing its current value |
| A credit note for the amount | Prices a quantity at the moment of the note, ignores tiers, leaves the aggregates, limits and analytics wrong, and is the operator's tool |
| Reopen the closed period (or amend its invoice) | A finalized invoice is a legal document (immutable by trigger, G3); late usage already is the repair the model has |
| Keep every event's customer, meter and quantity in the aggregator instead of ingest-svc | The largest table would grow by those columns; ingest-svc already holds the key row and owns the retention window a correction is bound to |
| Correct `MAX`, `LAST`, `UNIQUE` by re-aggregating the period | A replay is an operator's job and rebuilds a generation; a tenant call must not trigger one |

## Consequences

- The correction is its own route and request type, not two optional fields on an event, because an event's `event_time` is required in the OpenAPI schema and in every typed
  SDK; making it optional for corrections would have changed the type of `IncomingEvent.event_time` for all four SDKs.
- `idempotency_keys` gets four nullable columns (`customer_id`, `meter_code`, `event_time`, `quantity_units`), written for every event from the migration on: about 40 bytes a key,
  inside the margins of [ADR 0014](0014-idempotency-key-retention.md)'s measurement. `event_corrections` is a small table (one row per correction), kept; a sandbox reset clears it.
- `UsageEvent` gets field 20 (`correction`); fields 15 to 19 stay held. An old consumer that ignores unknown fields and rejects a negative quantity dead-letters a correction
  (`INVALID_EVENT`): consumers are deployed before ingest-svc starts to accept corrections (they are in one release).
- Five per-event result codes: `INVALID_CORRECTION`, `CORRECTION_UNKNOWN_EVENT`, `CORRECTION_MISMATCH`, `CORRECTION_UNSUPPORTED_METER`, `CORRECTION_OF_CORRECTION`.
