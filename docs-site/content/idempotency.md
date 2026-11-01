# Idempotency

A network call can fail after the server has done the work, so you retry, and a retry must not count twice or charge twice. Payrudder deduplicates with keys you choose. What is deduplicated, and how, depends on the call.

## Usage events

Every event in `POST /v1/events` can carry an `idempotency_key` (1 to 128 characters of letters, digits and `_ . : -`). The first time a key is seen the event is `accepted` and gets an `event_id`. Sending the same key again answers `duplicate` with the **original** `event_id` and counts nothing:

```json
{ "events": [{ "idempotency_key": "req_8f2c", "customer_external_id": "cus_42", "meter_code": "api_calls", "quantity": "1", "event_time": "2026-11-03T10:15:00Z" }] }
```

- A **live** key must send an `idempotency_key` with every event; an event without one is `rejected` with `IDEMPOTENCY_KEY_REQUIRED`. A **sandbox** key may leave it out, and then nothing is deduplicated.
- The same key with a *different* event is `rejected` with `IDEMPOTENCY_KEY_REUSED`: a key names one thing.
- Make the key from something that identifies the *thing that happened* and survives a retry: a request id, a message id, `<job id>:<step>`. Do not use a timestamp or a random value generated on each attempt, or every retry gets a new key and counts again.
- Keys are remembered for at least two months, longer than events can arrive late (an `event_time` older than 35 days is rejected), so a retry after a long outage is still recognised.
- The answer has one result per event, in order, and a bad event does not fail the batch: look at each `status` (`accepted`, `duplicate` or `rejected` with an `error_code`). Retry the whole request after a timeout or a `5xx`; retry only the rejected events if you fix them.

The SDKs retry `429` and `5xx` for you, with the same keys.

## Other calls

Four other operations take a key in an `Idempotency-Key` header (or, where the operation has one, an `idempotency_key` field of the body, which wins): granting credits, consuming credits, creating a payment link, and creating an upload session. A repeat with the same key and the same payload does not do the work twice: credits answer the current balance with `replayed: true`, a payment link answers the first link (without its token), an upload session answers the first session. The same key with another payload is a `409`.

Every other write is **not** deduplicated by a key. Where an object has a natural unique key, repeating the create is safe because it answers `409` instead of making a second object (a customer's `external_id`, a meter or plan `code`, a coupon code). Treat that `409` as "already done" after checking that the existing object is the one you meant. A write that has neither a key nor a natural unique key must not be retried blindly after a timeout: read the state first.

## In your own receiver

Webhook deliveries are at least once and not in order, so the same event can arrive twice. Use the event's `id` to ignore a repeat. See [Webhooks](webhooks.html).
