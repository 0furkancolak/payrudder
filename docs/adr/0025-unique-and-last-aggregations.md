# ADR 0025 — UNIQUE and LAST meter aggregations

- Status: Accepted
- Date: 2026-10-27

## Context

Meters aggregated by `SUM`, `COUNT` or `MAX`. Every layer combined the parts of a window (events of a batch, partitions, months, hours, a late period) by adding or keeping a
maximum, which is what makes the pipeline order-independent and replayable ([07-correctness.md](../07-correctness.md)). Two aggregations tenants ask for do not fit: **distinct values** of
an event property (active users per month) and **the latest value** of the period (seats at the end of the month). A distinct count is not additive (a customer in two months is one
customer), and "latest" needs an order across partitions and late arrivals and a rule for what a late event adds to a closed invoice.

## Decision

- **LAST** is the quantity of the event with the greatest `(event_time to the microsecond, event_id bytewise)` among the period's on-time events. Event ids are ULIDs assigned once at
  ingest, so the order is total. The aggregator stores the winning `(last_at, last_event_id)` beside the quantity, in the month row and in the hour rows, and merges by that order
  (idempotent, commutative, associative); limits-svc keeps `micros:id` beside the counter and compares in Lua by bytes; reconciliation repairs a counter to the aggregator's
  event. **A late event never changes a closed period**: it is not stored and not billed, because a latest value cannot be added to an invoice like a sum.
- **UNIQUE** is the number of distinct values of `value_property` per customer, meter and period. The aggregator stores the **set** of values (SHA-256 per value; `usage_distinct_values` per
  period, `usage_distinct_hours` per hour for 14 days) and keeps `usage_aggregates.quantity` and `late_usage.quantity` equal to the number of values in each state. The state of a value is
  the minimum of what its events say (`''` on time, else the earliest billing period of a late event), so arrival order does not matter and late usage stays additive (disjoint values), which
  keeps `Incremental(on-time + prior + late)` exact. Windows wider than a month, and day/week windows, are `count(DISTINCT)` over the sets. Billing reads exact counts; nothing approximate is
  on its path.
- **Enforcement** (limits-svc) keeps a Redis **set of 128-bit digests** per UNIQUE counter, bounded (`PAYRUDDER_UNIQUE_SET_CAP`, default 100 000), with the counter as the number of members
  so thresholds and `CheckEntitlement` are unchanged. A full set leaves new values to reconciliation, which sets the counter to the aggregator's exact count. This is an enforcement aid
  with documented error bounds (the reconciliation interval), not a source of truth.
- A code **cannot change into or out of UNIQUE or LAST** in a later version (plans-svc validation): the stored rows of a period have one meaning. A UNIQUE meter has `quantity_scale` 0, and ingest
  rejects an event of a UNIQUE meter without its property (`INVALID_PROPERTIES`).
- analytics-svc is unchanged: it totals raw event quantities and never claimed to be the aggregate. The console shows a dash for the total of these two meters on the usage page.

## Alternatives considered

| Option | Why not |
|---|---|
| HyperLogLog (aggregator or limits) | ~0.8 % error is fine for a dashboard and wrong for an invoice; it also cannot be decremented when a late value turns out to have been on time. Not used on the billing path. In Redis it would also have no exact correction path (a count cannot be written into an HLL), so the exact bounded set was kept |
| A distinct count per window kind and per entitlement | Needs the aggregator to follow entitlements and to backfill; the sets by month and hour make every window a `count(DISTINCT)` over a range, the way hour totals did for sums |
| Bill a late LAST event as a correction (`new − old`, possibly negative) | Needs negative late lines or correction invoices tied to a different answer for a closed period, and makes "what was the value of period P" depend on when you ask. Rejected for now: the tenant's grace period is the knob |
| Order LAST by Kafka offset or `ingested_at` | Offsets are per partition and `ingested_at` is not the event's own time; the tenant's event time with the id as the tie-break is what they mean by "latest" and is stable under replay |
| Store values instead of digests | A property can be 4 KiB and does not fit a primary key. SHA-256 collisions are not a consideration; the digest also keeps the personal data of a property (an email) out of the aggregates table |

## Consequences

- Storage grows with the number of distinct values (about 100 bytes per value per customer, meter and period, kept as long as the month rows). There is no cap on the exact side. A tenant with
  millions of values per customer per meter pays for it in the aggregator; the reads of a year or the lifetime window scan those rows.
- Late LAST events are lost to the invoice by design. Late UNIQUE values are billed. One case is not corrected: an on-time event for a value that a processed late event already put on an
  invoice moves the unit back to the period but does not amend the invoice (events processed out of ingest order across the cutoff).
- limits-svc memory per UNIQUE counter is at most the cap times about 40 bytes, for each window the entitlement counts over plus the month.
- A UNIQUE counter in Redis over-counts by a forgotten value after a Redis loss until the next reconciliation, and lags by the values it could not remember when its set is full.
- Schema: aggregator `usage_distinct_values`, `usage_distinct_hours` and `last_at` / `last_event_id` on the aggregate rows; plans-svc `meters.aggregation` accepts `UNIQUE` and `LAST`.
