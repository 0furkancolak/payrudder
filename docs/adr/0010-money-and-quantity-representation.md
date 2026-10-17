# ADR 0010 — Money and quantity representation

- Status: Accepted
- Date: 2026-10-17

## Context

Floats can't represent decimal money exactly, and their rounding errors accumulate across millions of events. Usage prices are often
**sub-minor-unit** (e.g. 0.002 kuruş per token), so integer minor units alone can't express unit prices. Single currency only.

## Decision

| Value | Representation |
|---|---|
| Invoice amounts, line amounts, fees, credits | `int64` **minor units** in Go (`money.Amount`), `bigint` in Postgres, `int64` in protobuf (`*_minor` fields) |
| Unit prices / tier prices | Decimal **strings** in protobuf and JSON (`"0.002"`), `numeric(30,12)` in Postgres, `github.com/cockroachdb/apd/v3` in Go |
| Event quantities | `int64` in the meter's **base unit** (tokens, requests, bytes). Fractional units are scaled by the tenant (e.g. send MB-seconds, not GB-hours) |
| Aggregated quantities | `numeric(38,0)` in Postgres (SUM overflow safety), `apd.Decimal` in Go when rating |
| Rounding | Compute each line in `apd` at full precision, then round **half-even** to an integer minor unit **once per line**. Invoice total = Σ rounded lines |
| Currency | One ISO 4217 code per tenant (default `TRY`), stored on invoices. No conversion |

Floats are forbidden in `money` and `rating` packages. A `forbidigo` lint rule bans `float32`/`float64` there.

## Alternatives considered

| Option | Why not |
|---|---|
| `float64` | Inexact. Non-starter for billing |
| `shopspring/decimal` | Popular, but slower and allocation-heavy. `apd` has explicit contexts (precision, rounding mode), which makes rounding policy visible |
| Integer "nano minor units" for prices | Avoids decimals but caps precision arbitrarily and makes tier configs unreadable |
| Decimal quantities on events | More flexible, but costlier on the hot path and the aggregation path. Integers in base units cover the common meters |

## Consequences

- Exact, reproducible totals: re-rating the same aggregates always yields identical cents.
- Line-level rounding means Σ lines may differ by at most 1 minor unit per line from a single global rounding. It's documented and explainable on the invoice.
- Tenants with fractional meters must scale their quantities (documented in the ingest API).
