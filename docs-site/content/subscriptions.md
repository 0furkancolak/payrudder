# Subscriptions

A subscription ties a **customer** to a **version of a package** and says when they start paying and how. `POST /v1/subscriptions` makes one: the customer's `customer_external_id`, a `plan_code`, and optionally a `plan_version` (0 or left out is the latest), a `start_time`, a `trial_days`, an `interval`, a `currency`, a `quantity` and a `coupon_code`. A customer has **at most one overlapping subscription**.

## States

`state` is one of:

| State | Meaning |
|---|---|
| `SUBSCRIPTION_STATE_TRIALING` | in the trial; no fixed fee until it ends (`trial_end_time`) |
| `SUBSCRIPTION_STATE_ACTIVE` | running |
| `SUBSCRIPTION_STATE_PAST_DUE` | a payment failed; the subscription is still billed ([Failed payments and retries](dunning.html)) |
| `SUBSCRIPTION_STATE_PAUSED` | the fixed fee is stopped until it is resumed; usage is still billed |
| `SUBSCRIPTION_STATE_CANCELLED` | ended (`end_time`) |

`cancel_at_period_end` is true while an end at the close of the current period is waiting. The webhook events for every step are in [Webhooks](webhooks.html) (`subscription.created`, `.changed`, `.paused`, `.cancelled`, `.trial_will_end`, …).

## What is billed, and when

- **Interval:** `SUBSCRIPTION_INTERVAL_MONTH` or `SUBSCRIPTION_INTERVAL_YEAR`. A new subscription renews on the day it started (its anniversary), not on the first of the month.
- **Fixed fee in advance** (`FEE_TIMING_ADVANCE`, the default): invoiced at the start of each period as its own invoice. **Usage in arrears:** one invoice for each calendar month, also for a yearly subscription, so usage is never held for a year. `FEE_TIMING_ARREARS` (monthly only) puts the fee on the invoice with the month's usage instead.
- **Seats:** `quantity` (1 to 100 000) multiplies the fixed fee; usage prices are not multiplied. Only for a subscription Payrudder runs and bills in advance.
- **Who runs it:** `owner` is `payrudder` or `provider`. A provider that renews subscriptions itself (Paddle) bills the fee; Payrudder mirrors what the provider does, and some of the calls below are refused for it. `provider` names the payment provider that sells it (the account's default when left out).

## Changing one

| Call | Effect |
|---|---|
| `POST /v1/subscriptions/{id}/change-preview` | what a change would cost, with the same calculation as the change, so the preview is what is then billed |
| `POST /v1/subscriptions/{id}/change` | move to another package version and/or interval, or change the seats |
| `POST /v1/subscriptions/{id}/change-plan` | the simpler call: move to another version now, prorating the fixed fee |
| `DELETE /v1/subscriptions/{id}/scheduled-change` | withdraw a change that is waiting for the end of the period |
| `POST /v1/subscriptions/{id}/pause`, `/resume` | stop the fixed fee and start it again |
| `POST /v1/subscriptions/{id}/cancel-at-period-end` | end at the close of the period; `DELETE` on the same path takes it back |
| `DELETE /v1/subscriptions/{id}` | end now, or at an `end_time`; with `credit_unused=true` the fee paid in advance for the rest of the period goes to the customer's credit balance |
| `POST /v1/subscriptions/{id}/coupon`, `DELETE` on the same path | redeem a coupon on a subscription that has none, take it off (the redemption is not given back) |

An **upgrade** or a change from monthly to yearly is immediate and prorated; a **downgrade** or a change from yearly to monthly waits for the end of the period the customer has paid for and shows on the subscription as a scheduled change. A change of `quantity` alone is immediate and prorated, more seats or fewer. Whatever moves money is invoiced, or becomes a credit balance when the new price is lower than the unused old time was worth ([Credits and balances](credits.html)). A change during a trial moves no money. `change` answers `409 ABORTED` if the fee of the current period has not been invoiced yet: try again in a moment.

An immediate change ends the subscription and starts its successor in one step; the new one's `changed_from_id` names the old one. Which version a subscription is on, and how to move many at once, is in [Packages and plans](packages-and-plans.html).

In the console this is **Subscriptions**: the list, a sheet for each, and the change dialog with the live preview.
