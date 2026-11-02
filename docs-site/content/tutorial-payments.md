# Tutorial: let a customer pay

Payrudder collects the money for the invoices it makes, through the payment provider you connected. A customer needs a page to pay on, and you have three ways to give it one, all without building a form. This tutorial makes each in the sandbox. You need a sandbox key and a customer that has an invoice ([usage-based billing, end to end](tutorial-usage-billing.html) makes one).

```sh
export BASE=https://YOUR-API-ADDRESS
export PAYRUDDER_API_KEY=qk_test_…
api() { curl -sS -H "Authorization: Bearer $PAYRUDDER_API_KEY" -H "Content-Type: application/json" "$@"; }
INVOICE_ID=…        # the id of an invoice that is open
```

## A link to one invoice

An invoice that is finalized and not paid can be opened by its customer without signing in:

```sh
api -X POST $BASE/v1/invoices/$INVOICE_ID/links -d '{}'
```

The answer has the `url`. It shows the invoice as a page (and its PDF once it is rendered) in English, Turkish, German, French, Spanish or Arabic (`?lang=` or the browser's language). The token is unguessable and **returned once**; the link works for 90 days by default (`expires_in_days`, at most 365) or until you revoke it with `DELETE /v1/invoices/{id}/links`.

## A link to a payment

A **payment link** is a page whose only job is to take a payment. For an invoice that is open:

```sh
api -X POST $BASE/v1/payment-links -H "Idempotency-Key: pay-inv-1" -d '{"kind":"invoice","invoice_id":"'"$INVOICE_ID"'"}'
```

or for an amount you name, with no invoice yet (the invoice is made when the customer presses Continue):

```sh
api -X POST $BASE/v1/payment-links -d '{"kind":"adhoc","amount_minor":"2500","description":"Onboarding","customer_external_id":"cus_42"}'
```

Both answer with the `url` and the `token` (once). An ad-hoc link is single-use unless you say `"single_use": false`; the `Idempotency-Key` makes a retry return the link made the first time instead of a second one. When the customer pays, `payment_link.paid` and `invoice.paid` are sent to your [webhook endpoints](tutorial-webhooks.html): fulfil the order on the event, not on the redirect.

## The customer's own page

```sh
api -X POST $BASE/v1/customers/cus_42/portal-links -d '{}'
```

The `url` shows the customer's invoices, payments, PDFs and receipts, read-only, with a pay button on the open ones. It works for 30 days by default (at most 365) or until you revoke them with `DELETE /v1/customers/{external_id}/portal-links`. Put it behind a button in your own application ("Billing") and make a fresh one when the customer clicks, since the token is only shown when it is made.

## In the sandbox

The pages are the real ones; the payment goes to the simulator. Choose the outcome with the customer's payment method or `POST /v1/sandbox/customers/{external_id}/payment-scenario`, so you can see a decline ([Test mode](test-mode.html)). Nothing reaches a real card or a real mailbox.

## Things to know

- A customer you have **blocked** cannot start a new purchase; blocking and unblocking are in the [API reference](api/customers.html).
- The language of the page is the customer's `locale` (`en`, `tr`, `de`, `fr`, `es`, `ar`), else the browser's.
- Amounts are minor units as strings (`"2500"` is 25.00 in a two-decimal currency).
- A link you made and no longer want: revoke it (`payment_link.expired` is sent when one runs out unpaid).
