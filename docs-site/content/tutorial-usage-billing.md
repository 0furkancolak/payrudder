# Tutorial: usage-based billing, end to end

You will bill one customer for what it used, in the sandbox, from the first call to a paid invoice. It takes about ten minutes and uses only `curl`, so nothing here depends on a language. Every step names the page that explains it.

You need a **sandbox** key (`qk_test_…`, see [Getting started](getting-started.html)) and your API address:

```sh
export BASE=https://YOUR-API-ADDRESS            # http://localhost:42800 on a local stack
export PAYRUDDER_API_KEY=qk_test_…
api() { curl -sS -H "Authorization: Bearer $PAYRUDDER_API_KEY" -H "Content-Type: application/json" "$@"; }
```

Nothing below touches real money: a sandbox key reaches the simulator ([Test mode](test-mode.html)).

## 1. Say what you count

```sh
api $BASE/v1/meters -d '{"code":"api_calls","aggregation":"AGGREGATION_SUM","value_property":"n","quantity_scale":0,"unit_label":"calls"}'
```

A `SUM` meter adds up the quantities of its events; `quantity_scale` is how many decimal places a quantity may have, and cannot change later ([Usage metering](usage-metering.html)).

## 2. Say what it costs

A **package** (plan) is created once, then gets immutable **versions** that hold the prices:

```sh
api $BASE/v1/plans -d '{"code":"starter","name":"Starter"}'

api $BASE/v1/plans/starter/versions -d '{
  "base_fee_minor": "1000",
  "prices": [{ "meter_code": "api_calls", "per_unit": { "unit_price": "0.01" } }]
}'
```

`base_fee_minor` is the fixed fee in the currency's minor units (cents), as a string. `per_unit` prices each unit of the meter; graduated tiers, packages and an included allowance are in the [`PriceInput`](api/plans.html) model. Publishing again makes version 2; customers stay on the version they subscribed to.

## 3. Add the customer and subscribe it

```sh
api $BASE/v1/customers -d '{"external_id":"cus_42","name":"Acme Ltd","email":"billing@acme.example","payment_method":"pm_sandbox_success"}'

api $BASE/v1/subscriptions -d '{"customer_external_id":"cus_42","plan_code":"starter","fee_timing":"FEE_TIMING_ARREARS"}'
```

`external_id` is **your** id for the customer; every later call uses it. The `pm_sandbox_*` payment method tells the simulator how the payment goes (this one succeeds; [Test mode](test-mode.html) lists the others). `FEE_TIMING_ARREARS` bills the fixed fee together with the month's usage, so there is one invoice to look at.

## 4. Send usage

```sh
EVENT_TIME=$(date -u -v-1M +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '1 minute ago' +%Y-%m-%dT%H:%M:%SZ)

api $BASE/v1/events -d '{"events":[{"customer_external_id":"cus_42","meter_code":"api_calls","quantity":"100","event_time":"'"$EVENT_TIME"'","idempotency_key":"tutorial-1"}]}'
```

The answer has one result per event: `accepted`, `duplicate` (send the same `idempotency_key` again and see) or `rejected` with an `error_code` ([Errors](errors.html#event-codes)). `event_time` is when the usage happened; it decides the billing period. The same thing from the [command line](cli.html): `payrudder events send --customer cus_42 --meter api_calls --quantity 100`.

Read it back (usage is counted a moment after it is accepted):

```sh
api $BASE/v1/customers/cus_42/usage
```

## 5. Close the month now

A real invoice waits for the month to end. In the sandbox you do not have to:

```sh
api -X POST $BASE/v1/sandbox/close-period -d '{}'
```

It answers `202` and runs the real billing flow: cutoff, rating, numbering (`SBX-…`) and the payment saga against the simulator. Poll for the invoice:

```sh
api "$BASE/v1/invoices?customer=cus_42"   # note the id of the invoice: INVOICE_ID=…
```

The invoice has a `BASE_FEE` line and a `USAGE` line for `api_calls`, and its `status` moves to `paid` when the simulated payment succeeds. Totals are integers in minor units, as strings. `payrudder invoices list --customer cus_42` shows the same.

## 6. Try a declined payment

Repeat steps 3 to 5 with a second customer (`cus_43`) whose `payment_method` is `pm_sandbox_declined`; `GET /v1/sandbox/scenarios` lists every outcome the simulator knows. Its invoice is not paid, and Payrudder retries on the compressed sandbox schedule. To run the payment again under another outcome:

```sh
api -X POST $BASE/v1/sandbox/invoices/$INVOICE_ID/simulate -d '{"scenario":"success"}'
```

Your application learns about a payment, paid or not, from [webhooks](tutorial-webhooks.html), not by polling.

## Where next

- [Tutorial: receive webhooks on your machine](tutorial-webhooks.html), to be told when the invoice is paid.
- [Tutorial: let a customer pay](tutorial-payments.html), to send the customer to a page where they pay.
- [Idempotency](idempotency.html) before you send live usage: a live key requires an `idempotency_key` on every event.
- The [SDKs](sdks.html) wrap every call above; the console's **Connect your app** page writes the code for your own meter.
