# ADR 0010 — Money and quantity representation

- Status: Accepted (revised 2026-10-17: fractional quantities, tenant-chosen currency)
- Date: 2026-10-17

## Context

Floats can't represent decimal money exactly, and their rounding errors accumulate across millions of events. Usage prices are often
**sub-minor-unit** (e.g. 0.002 kuruş per token), so integer minor units alone can't express unit prices.

Meters can be **fractional** (GB-hours, CPU-seconds, kWh), so event quantities must accept decimals. At the same time, the hot paths
(aggregation upserts, Redis counters in limits-svc) need exact, cheap arithmetic. Redis has no exact decimal increment
(`INCRBYFLOAT` is a double).

Each tenant picks its own currency when it is created. A tenant still bills in exactly one currency (no multi-currency, no FX).

## Decision

| Value | Representation |
|---|---|
| Invoice amounts, line amounts, fees, credits | `int64` **minor units** in Go (`money.Amount`), `bigint` in Postgres, `int64` in protobuf (`*_minor` fields) |
| Unit prices / tier prices | Decimal **strings** in protobuf and JSON (`"0.002"`), `numeric(30,12)` in Postgres, `github.com/cockroachdb/apd/v3` in Go |
| Meter precision | Each meter declares `quantity_scale` (0–6 decimal places) at creation. Immutable across meter versions |
| Event quantities (API) | Decimal **string** or JSON number, parsed exactly (`json.Number` → `apd`), never through `float64`. At most `quantity_scale` decimal places, otherwise `INVALID_QUANTITY_PRECISION` |
| Event quantities (internal) | `int64` **scaled units**: `quantity_units = quantity × 10^scale` (e.g. `1.25` GB-h with scale 3 → `1250`). Carried as `quantity_units` + `quantity_scale` on `UsageEvent` |
| Aggregated quantities | Scaled units, `numeric(38,0)` in Postgres (SUM overflow safety), `int64` in Redis counters with an overflow guard in Lua |
| Limits and tier boundaries | Decimal strings in the API, stored as scaled units of the meter |
| Rating | `quantity = units × 10^−scale` in `apd`, then line amount = quantity × price at full precision |
| Rounding | Round **half-even** to an integer minor unit **once per line**. Invoice total = Σ rounded lines |
| Currency | ISO 4217 code chosen by the tenant at creation from the active ISO 4217 list (`pkg/money`, phase 40), **immutable**. The minor-unit exponent comes from a static table (`TRY`/`USD`/`EUR` 2, `JPY`/`KRW` 0, `KWD`/`BHD` 3). Stored on every invoice. No conversion |

Floats are forbidden in the `money`, `quantity` and `rating` packages. A `forbidigo` lint rule bans `float32`/`float64` there.

## Alternatives considered

| Option | Why not |
|---|---|
| `float64` | Inexact. Non-starter for billing |
| `shopspring/decimal` | Popular, but slower and allocation-heavy. `apd` has explicit contexts (precision, rounding mode), which makes rounding policy visible |
| Integer "nano minor units" for prices | Avoids decimals but caps precision arbitrarily and makes tier configs unreadable |
| Integer base units only (tenant scales) | Simplest, but pushes unit conversion to every client and makes invoices show "1,250,000 MB-s" instead of "1.25 GB-h" |
| `numeric` decimals end to end | Exact, but Redis counters would need string arithmetic in Lua, and every upsert pays decimal cost. Scaled `int64` is exact for the declared precision at integer cost |
| Mutable currency | Breaks old invoices and credit balances. A tenant that wants another currency creates a new tenant |

## Consequences

- Exact, reproducible totals: re-rating the same aggregates always yields identical minor units.
- Line-level rounding means Σ lines may differ by at most 1 minor unit per line from a single global rounding. It's documented and explainable on the invoice.
- Precision is per meter and fixed. A meter that needs more decimals is a new meter code.
- Headroom: `int64` at scale 6 still holds 9.2 × 10¹² units per event. Aggregates are `numeric(38,0)`. Redis counters reject an increment
  that would overflow and the event is still counted in aggregator (limits are advisory, [ADR 0006](0006-eventual-consistency-for-limits.md)).
- `quantity_scale` travels with each event, so replays and consumers never need to look the meter up to interpret a quantity.
