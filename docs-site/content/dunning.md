# Failed payments and retries

When a customer's payment is declined, Payrudder does not give up at once. **Dunning** is the schedule on which it tries again, and what happens when the schedule runs out. It applies to invoices that Payrudder collects; a subscription a payment provider runs itself (Paddle) is retried by the provider on the provider's own rules.

## The schedule

`GET /v1/dunning-settings` returns `retry_hours`, the delays between attempts, and `defaults_in_use`, true when you have set nothing and the deployment's delays apply. `PUT /v1/dunning-settings` replaces them: **up to six delays, each 1 to 720 hours** (minutes in a sandbox account, so a test shows the whole cycle at once). A declined charge is retried after the first delay, then after the second, and so on. **When the delays run out the invoice is given up as `uncollectible`**; an empty list gives up on the first decline.

A decline that the provider says must not be tried again ends the schedule at once, whatever attempts are left. Today that is Stripe's `do_not_try_again` advice.

## What the customer and you see

- The invoice stays `open` while attempts remain; after the last it is `uncollectible` (webhook `invoice.uncollectible`) and the customer gets the payment-failed mail. The page the customer opens for an uncollectible invoice says it could not be collected and asks them to contact you ([Invoices](invoices.html)).
- A subscription whose payment failed can show `SUBSCRIPTION_STATE_PAST_DUE` ([Subscriptions](subscriptions.html)); it is still billed, and a change of package is refused while it is past due.
- `GET /v1/reports/dunning` lists the invoices whose payment has not gone through, with the customer's external id and the provider's last error. In the console this is the dunning report.

## Chargebacks

`block_on_chargeback` in the same settings (off unless you turn it on) blocks a customer, with the category `chargeback`, as soon as their bank opens a formal dispute: no new purchases, and their open invoices can no longer be paid. It never lifts by itself; you lift it on the customer. An inquiry does not block. The disputes themselves are `GET /v1/disputes` and the webhook `payment.disputed` ([Webhooks](webhooks.html)).

In the console this is the page named **Tax and retries** ([Meters, packages and pricing](console-pricing.html)).
