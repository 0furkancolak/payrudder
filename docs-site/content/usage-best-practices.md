# Sending usage well

Rules that keep usage correct when you send it from production code. Each links to the page that explains the rule in full.

## Send in batches

`POST /v1/events` takes up to 500 events and at most 1 MiB per request. Buffer events for a short time (a second or a few) and send them together instead of one call per event. A bad event does not fail the batch: read the `status` of each result, in order. See [Usage metering](usage-metering.html).

## Give every event a key that survives a retry

A live key must send an `idempotency_key` with every event. Build it from the thing that happened (a request id, a message id, `<job id>:<step>`), never from a timestamp or a value generated on each attempt, or a retry counts twice. The same key with a different event is rejected. See [Idempotency](idempotency.html).

## Retry the right things

- After a timeout or a `5xx`, retry the whole request with the same keys.
- On `429`, wait the seconds in `Retry-After`. The SDKs do both for you.
- Retry only the `rejected` events of a batch, and only after you fixed them. An `INVALID_PROPERTIES` or `IDEMPOTENCY_KEY_REUSED` result will fail again unchanged.

## Stamp the time the usage happened

`event_time` is when the usage happened, by your clock, and it decides the billing period. Do not use the time you send it. Keep your servers' clocks right: an event more than 5 minutes in the future or older than 35 days is rejected. Events that arrive after a period's cutoff are billed as an adjustment on the next open invoice, so a queue that is stuck for days moves money between invoices.

## Create the customer first

`customer_external_id` must exist before its first event. Create customers when they sign up, not when they first use the product.

## Send quantities as strings

Send `"1.250"`, not `1.25`. A string cannot be changed on the way, and the number of decimal places may not exceed the meter's scale.

## Fix mistakes with a correction

An accepted event cannot be edited or deleted. Send a correction (`POST /v1/events/corrections`) while the event's key is still remembered. It only works for `SUM` and `COUNT` meters without a filter.

## Do not use limits as a to-the-event cutoff

Limits are read from a fast counter that is reconciled with the exact figures, so they are eventually consistent. Keep your own hard stop if a single event must never go over.

## Try it in test mode first

A sandbox key may leave the idempotency key out and nothing is deduplicated, so a script that works in the sandbox can still fail with `IDEMPOTENCY_KEY_REQUIRED` on a live key. Send keys in the sandbox too. See [Test mode](test-mode.html).
