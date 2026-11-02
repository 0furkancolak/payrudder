# Usage metering

Payrudder turns what your customers use into invoices. Three things carry that: **meters** say what is counted, **usage events** are the facts you send, and **periods** decide which invoice a fact lands on.

## Meters

A meter has a `code` (lower case, digits and `_`, for example `api_calls`), an `aggregation`, and a `quantity_scale` (0 to 6 decimal places, fixed once the code exists). How events add up is the aggregation:

| Aggregation | The quantity of a period is |
|---|---|
| `SUM` | the sum of the event quantities (API calls, tokens, gigabyte-hours) |
| `COUNT` | the number of events |
| `MAX` | the largest event quantity of the period |
| `UNIQUE` | the number of **distinct values** of the event property named by `value_property` (active users in a month); its scale is 0 |
| `LAST` | the quantity of the latest event of the period (seats at the end of the month) |

A `filter` can limit a meter to events whose properties have given values. A meter is **immutable**: changing it makes a new version, and a code cannot change into or out of `UNIQUE` or `LAST` afterwards. `unit_label` and `category` are free text for your own use and for the invoice.

## Events

An event says: customer *C* used meter *M* by quantity *Q* at time *T*. Send them in batches of up to 500 (at most 1 MiB) to `POST /v1/events`:

- `quantity` is an exact decimal with at most the meter's scale decimal places. Send it as a **string** (`"1.250"`): a JSON number is parsed as written, never as a float, but a string cannot be mangled on the way.
- `event_time` is when the usage **happened**, by your clock. It decides the billing period. It may not be older than 35 days or more than 5 minutes in the future.
- `properties` carries the values a `UNIQUE` meter counts (its `value_property`) and anything a meter's `filter` looks at (at most 32 entries and 4 KiB). A `UNIQUE` event without its property is rejected with `INVALID_PROPERTIES`.
- `customer_external_id` is your own id for the customer; create the customer first (`POST /v1/customers`).
- Events are facts: there is no call to edit or delete one, and a quantity cannot be negative. To correct what a customer was billed, issue a credit note.

To make retries safe, send an `idempotency_key` (see [Idempotency](idempotency.html)).

## Periods, grace and late events

A **billing period** is a calendar month in the account's time zone, written `YYYY-MM`. Every event is stamped with the period of its `event_time`. After the month ends there is a **grace period** you choose for the account (0 to 7 days); the period's **cutoff** is its end plus the grace.

- An event of a period that Payrudder received **before the cutoff** is *on time*: it is part of that period's invoice.
- An event received **after the cutoff** is *late*: the closed period does not change; the usage is billed as an adjustment line on the next open invoice. (A `LAST` meter is the exception: a late event never changes a closed period, because a latest value cannot be added to an invoice.)

`GET /v1/customers/{external_id}/usage?period=2026-11` returns the on-time usage of a customer in a period, per meter, as exact decimals; late usage is not in it.

## Correcting an event

An accepted event is never edited. To fix one, send a **correction**: `POST /v1/events/corrections` with the `idempotency_key` of the event (`corrects`), the quantity it should have had (`0` cancels it), a `reason`, and a key of its own. Payrudder works out the difference from what the event stands at now and adds it to the **period of the original event**, by the rules above: while that period is open (or inside its grace period) the usage changes exactly; after the cutoff the difference is late usage, a (negative, when you take usage back) adjustment line on the next open invoice, priced on top of what the period already cost so tiers stay right. If you take back more than that invoice charges, the invoice charges nothing and the rest (tax included) is added to the customer's credit balance, which pays their next invoices. A finalized invoice never changes. A retry of the same correction changes nothing.

What can be corrected: events of `SUM` and `COUNT` meters without a property filter, while the event's idempotency key is still remembered (at least 45 days, less than 76). A `COUNT` event is one whatever its quantity, so you can cancel it (`0`) or count it again. `MAX`, `LAST` and `UNIQUE` meters cannot be corrected. If a correction takes back more than the next invoice charges, that invoice is `0`; the rest is not carried forward, so issue a credit note or grant credit for it.

## The upcoming invoice

`GET /v1/customers/{external_id}/upcoming-invoice` shows what the customer's next regular invoice would be if it were drafted now: usage and late usage so far, the fixed fee, tax and any credit balance, as the real invoice computes them. It is an estimate (`estimate: true`) with no id or number; it changes with every event until the period closes, and it leaves out a fixed fee that is invoiced in advance as its own invoice.

## From usage to money

A **package** (plan) gives each meter a price (per unit, graduated tiers, package or flat) and optional limits. A customer subscribed to a package gets an **invoice** at the end of the period: the usage is rated with the prices of the plan version the subscription was on, plus any fixed fee. Amounts are integers in the currency's minor unit (cents), sent as strings in JSON. Limits are enforced from a fast counter that is reconciled with the exact figures, so they are eventually consistent: do not rely on them as a to-the-event cutoff.
