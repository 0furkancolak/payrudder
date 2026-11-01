# Test mode

Every app has two environments: a **sandbox**, where nothing is real, and **live**. You integrate against the sandbox, then switch the key and the address of the code to live.

## What the sandbox is

- A `qk_test_` key reaches the sandbox, a `qk_live_` key reaches live. Same API, same code, a different key. The two share no data.
- Payments run against a **simulator**, never a real provider, and nothing sent from the sandbox reaches a real customer: e-mails carry a `[SANDBOX]` prefix and go to a sink, invoices have their own number series (`SBX-…`) and a watermark on the PDF.
- The sandbox does not wait for the month to end: you can close a period now, and the retry schedule of failed payments (dunning) is compressed to minutes.
- `idempotency_key` is optional on events in the sandbox (live requires it): include it anyway, so your code is the code you ship.

## Choosing how a payment goes

Give a sandbox customer a payment method from `GET /v1/sandbox/scenarios`, or set it with `POST /v1/sandbox/customers/{external_id}/payment-scenario` (for example `{"scenario": "declined"}`). Each `pm_sandbox_*` method makes the payment take one path, so you can see how your app behaves when a charge succeeds, is declined, is declined once and then succeeds, fails with an error and then succeeds, or times out whether or not it went through.

## Getting an invoice now

1. Create a customer, subscribe it to a package, and send some usage.
2. `POST /v1/sandbox/close-period` closes the period right away and runs the real billing flow: cutoff, rating, numbering and the payment saga with the simulator. It answers `202`; poll `GET /v1/invoices` until the invoice is there.
3. To run a payment again under another scenario, `POST /v1/sandbox/invoices/{id}/simulate`. It sets the customer's scenario and retries the invoice's payment.

Webhooks are sent from the sandbox too, so you can test your receiver end to end; **Send a test webhook** on the console's *Connect your app* page sends a signed `ping` to an endpoint (see [Webhooks](webhooks.html)).

## What only live does

- A live environment starts **inactive** and is switched on by a go-live checklist (the seller's billing profile, a connected payment provider, accepted terms). Until then it has no keys: creating one is refused with `plans.api_key.tenant_not_active`.
- Real payment providers, real customer e-mail and the live invoice numbers.
- The sandbox routes (`/v1/sandbox/*`) answer `403` to a live key.

The console can **promote** the sandbox configuration to live so you do not rebuild it by hand: meters, plans and their prices, tax rates, coupons, mail settings and webhook endpoints (never customers, usage, invoices or payments). It only adds, matches objects by their code, so running it again does nothing, and a dry run shows the difference first. Test the same flow once more in live with a small real payment before you send customers there.
