# payrudder (Python SDK)

Python client for the [Payrudder](../../README.md) tenant API (`/v1/*`) and for verifying webhook deliveries.

> **Pre-release (`0.0.0`).** Nothing is published to PyPI and no version is tagged until the whole platform is finished.
> Install from this repository: `pip install ./sdk/python` (or `pip install -e ./sdk/python[dev]` to work on it).

- Python 3.10+, fully typed (`py.typed`, checked with `mypy --strict`).
- A sync client (`Payrudder`) and an async client (`AsyncPayrudder`) with the same methods.
- One runtime dependency: [`httpx`](https://www.python-httpx.org/).
- Models are `TypedDict`s with the wire's snake_case names; money (`*_minor`) is a Python `int`.

Scope: the tenant API authenticated with an API key (`qk_test_...` for the sandbox, `qk_live_...` for live). The operator
(`/admin/v1`) and console (`/app/v1`, `/auth`) routes are not part of this SDK.

## Quickstart

```python
from payrudder import Payrudder

client = Payrudder(api_key="qk_test_...", base_url="http://localhost:42800")
# or set PAYRUDDER_API_KEY and PAYRUDDER_BASE_URL and call Payrudder()

client.customers.create_customer({"external_id": "cus_42", "name": "Acme", "email": "billing@acme.test"})

results = client.events.ingest(
    [
        {
            "customer_external_id": "cus_42",
            "meter_code": "ai_tokens",
            "quantity": "1500",
            "event_time": "2026-11-03T10:15:00Z",
        }
    ]
)
print(results[0]["status"])  # accepted | duplicate | rejected

usage = client.customers.get_customer_usage("cus_42", period="2026-11")
```

Async:

```python
import asyncio
from payrudder import AsyncPayrudder


async def main() -> None:
    async with AsyncPayrudder() as client:
        invoice = await client.invoices.get_invoice("0192...")
        async for customer in client.customers.iter_customers(page_size=200):
            print(customer["external_id"])


asyncio.run(main())
```

### Client options

| Option | Default | |
|---|---|---|
| `api_key` | `PAYRUDDER_API_KEY` | required one way or the other |
| `base_url` | `PAYRUDDER_BASE_URL`, else `http://localhost:42800` | |
| `timeout` | `30` seconds | per request |
| `max_retries` | `2` | see [Retries](#retries) |
| `http_client` / `transport` | | your own `httpx.Client` / `httpx.AsyncClient`, or a transport (tests, proxy, mTLS) |
| `user_agent` | `payrudder-python/0.0.0` | |
| `default_headers` | | sent on every request |

Every method also takes `options={"timeout": ..., "max_retries": ..., "idempotency_key": ..., "headers": {...}}`.
Use the client as a context manager (`with` / `async with`) or call `close()` / `await aclose()`.

## Methods

One method per `operationId` of [`api/openapi/v1.yaml`](../../api/openapi/v1.yaml), named `snake_case(operationId)` and grouped
under the first path segment: `POST /v1/customers` (`createCustomer`) is `client.customers.create_customer(...)`,
`GET /v1/e-invoices/{id}` (`getEInvoice`) is `client.e_invoices.get_e_invoice(id)`. Path parameters are positional, a JSON
body is the next positional argument (a `TypedDict` from `payrudder.models`), query parameters are keyword-only (`from` is
`from_`). Responses are the typed dicts of the spec; a CSV export returns `str`, the inbox file `bytes`, a `204` `None`.

A test (`tests/test_spec_sync.py`) parses the OpenAPI file and fails if an operation has no method or a method has no
operation, so the SDK cannot drift. The models, the resources and the int64 table are generated:

```sh
python tools/generate.py          # rewrite src/payrudder/{models,resources,_wire_tables,_opids}.py
python tools/generate.py --check  # what the test and CI run
```

### Money and decimals

`*_minor` values are int64 strings on the wire and `int` in Python, in both directions (never a float). Quantities and
prices are decimal strings: pass a `str`, `int` or `Decimal`; a `float` is refused with `TypeError`. `datetime` / `date`
are accepted where the API takes a timestamp or a date (a naive `datetime` is taken as UTC).

### Ingesting events

`client.events.ingest(events)` takes any number of events, splits them into valid batches (1 to 500 events, under 1 MiB),
fills a missing `idempotency_key` with a generated one, and returns one result per event in input order. A single rejected
event is `{"status": "rejected", "error_code": ...}` in the list, not an exception; a whole-batch failure (401, 429, 503, ...)
raises. `client.events.ingest_events({"events": [...]})` is the raw one-request call.

Live keys require an `idempotency_key` per event, and the generated one is stable across the automatic retries of a batch.
If you need to resume a half-done multi-batch call safely, pass your own keys.

### Pagination

Every list operation that returns `next_page_token` has a lazy generator next to the raw call:

```python
page = client.invoices.list_invoices(page_size=100)  # one page, with next_page_token
for invoice in client.invoices.iter_invoices():  # every invoice
    ...
async for payout in client.affiliates.iter_affiliate_payouts():  # async client
    ...
```

## Errors

```python
from payrudder import APIError, NotFoundError, RateLimitError, ValidationError, APIConnectionError

try:
    client.customers.get_customer("nope")
except NotFoundError as e:
    print(e.status_code, e.code, e.key, e.message, e.request_id)  # 404 NOT_FOUND common.not_found ...
except ValidationError as e:
    print(e.details)  # [{"field": "email", "description": "..."}]
except RateLimitError as e:
    print(e.retry_after)  # seconds, from Retry-After
except APIError as e:  # any other error status
    ...
except APIConnectionError:  # no answer (APITimeoutError is a subclass)
    ...
```

All errors derive from `PayrudderError`. `APIError` carries `status_code`, `code` (gRPC style), `key` (the stable,
translatable error key such as `billing.credit_note.nothing_to_refund`: translate the key, not the message), `message`,
`details`, `request_id`, `retry_after`, `headers` and `body`. Subclasses: `AuthenticationError` (401),
`PermissionDeniedError` (403), `NotFoundError` (404), `ConflictError` (409), `ValidationError` (400, 422),
`RateLimitError` (429), `ServerError` (5xx).

## Retries

Automatic, with exponential backoff (0.5 s doubling, capped at 8 s, jittered):

- network errors: a connection that never opened is always retried; one that may have carried the request (read timeout,
  dropped connection) only when the call can safely repeat;
- `429`, honouring `Retry-After` (a value over 60 s is not waited for: you get the `RateLimitError`);
- `502`, `503`, `504`;
- `500` only for idempotent calls: `GET`, `PUT`, `DELETE`, and a `POST` that carries an idempotency key.

Other `4xx` are never retried. `max_retries=0` turns it all off.

## Idempotency

Pass `options={"idempotency_key": "order-77"}` to send an `Idempotency-Key` header (the API honours it on payment links
and upload sessions, which also accept `idempotency_key` in the body). A POST with a key is retried after a `500`, and the
same key is sent on every attempt. Event ingestion has a key per event (see above).

## Webhooks

Verify the **raw** request body before parsing it (a re-serialised JSON will not match). `construct_event` verifies and
parses; a bad, missing or stale (more than 5 minutes) signature raises `WebhookSignatureError`.

```python
from payrudder import EventType, WebhookSignatureError, construct_event


# Flask
@app.post("/webhooks/payrudder")
def hook():
    try:
        event = construct_event(request.get_data(), request.headers.get("Payrudder-Signature"), WEBHOOK_SECRET)
    except WebhookSignatureError:
        return "", 400
    if event["type"] == EventType.INVOICE_PAID:
        total = event["data"]["total_minor"]  # int
    return "", 200
```

`verify_webhook(payload, header, secret, tolerance=300)` only verifies. A delivery signed while a secret rotates carries two
`v1` signatures and verifies with either the new or the old secret. Deliveries are at least once and unordered: ignore a
repeated `event["id"]`. `EVENT_TYPES` and `EventType` list the event names of [docs/16-webhooks.md](../../docs/16-webhooks.md).

## Development

```sh
python -m venv .venv && .venv/bin/pip install -e '.[dev]'
.venv/bin/ruff check . && .venv/bin/ruff format --check .
.venv/bin/mypy                       # strict
.venv/bin/pytest                     # unit tests against a local fake gateway; no network, no stack needed
PAYRUDDER_API_KEY=qk_test_... .venv/bin/pytest tests/test_integration.py   # optional smoke test against a running stack
.venv/bin/python -m build            # sdist and wheel in dist/
```

## Licence

Elastic License 2.0, the same as the rest of the repository: see [LICENSE](../../LICENSE).
