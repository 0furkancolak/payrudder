# Errors

Every error of the tenant API has the same shape, whatever went wrong:

```json
{
  "error": {
    "code": "INVALID_ARGUMENT",
    "key": "gateway.request.invalid",
    "message": "…",
    "details": [{ "field": "events", "description": "…" }],
    "request_id": "…"
  }
}
```

- **`key`** is stable and always there: dotted, the service first (`billing.credit_note.not_paid`). An error with no key of its own gets `common.<reason>`. **Translate the key, not the message**: the `message` is English text for a log, and its wording can change; keys do not.
- **`code`** is the class of the error and decides the HTTP status.
- **`details`** points at the fields that were wrong; it is left out when there is nothing to point at.
- **`request_id`** is the `X-Request-Id` of the call. Quote it when you ask for help.

## Status and what to do

| Code | HTTP | Meaning | Retry? |
|---|---|---|---|
| `INVALID_ARGUMENT` | 400 | the request is malformed or a field is not valid | no: fix the request |
| `UNAUTHENTICATED` | 401 | the key is missing, wrong or ended | no: see [Authentication](authentication.html) |
| `PERMISSION_DENIED` | 403 | the key may not do this, or the environment is not open for it | no |
| `NOT_FOUND` | 404 | there is no such object | no |
| `ALREADY_EXISTS`, `ABORTED`, `FAILED_PRECONDITION` | 409 | the object exists, or its state does not allow this now | no: read the state, then decide |
| `REQUEST_TOO_LARGE` | 413 | the body is over 1 MiB (usage events: also over 500 events) | no: send less per request |
| `RESOURCE_EXHAUSTED` | 429 | you are over a rate limit or a quota | yes, after the seconds in `Retry-After` |
| `UNAVAILABLE` | 503 | a dependency is not ready | yes, with the same [idempotency key](idempotency.html) |
| `DEADLINE_EXCEEDED` | 504 | the call took too long | yes, with the same idempotency key |

An `INTERNAL` error never carries its cause in the message, and neither does an `UNAVAILABLE`, `DEADLINE_EXCEEDED`, `RESOURCE_EXHAUSTED` or `ABORTED` error that comes from the network or a database rather than from Payrudder's own rules: its `message` is a plain sentence that quotes the `request_id`, and the `key` (`common.unavailable`, `common.timeout`, `common.rate_limited`, `common.conflict`) is what to branch on. An error Payrudder wrote itself for these codes (a quota, an invitation limit) keeps its own message. A request that timed out may or may not have been done: retry it only with an idempotency key, or read the state first. The SDKs retry `429` and `5xx` for you with the same key.

A usage event is judged on its own inside a `202` answer: an event that cannot be counted is `rejected` with an `error_code` (the [list is below](#event-codes)) and does not fail the others. See [Usage metering](usage-metering.html).

You do not have to catch a rejection in the answer to see it later. Every rejected event is kept for **14 days** (at most **10 000 per environment**, the oldest dropped first) and can be read back: the **Rejected events** section of the Connect page in the console, with the hint for each code, or `GET /v1/rejected-events` (filter by `error_code` and `meter`, page with `page_token`) and `GET /v1/rejected-events/summary` (the last 24 hours by code and by hour), for a key with the `usage:read` permission. A row holds the code and the message, the meter code and the customer id as you sent them, the event time and the idempotency key; the **properties and the quantity are never stored**. A row is written just after the answer, best effort, so it can be a moment late and, under a burst of rejections or a database problem, a rejection can be missing from the list; the answer you got is the record. The sandbox's log is emptied by a sandbox reset.

## In the SDKs and the command line

The SDKs turn the status into one typed error and keep the fields of the envelope on it (status, `code`, `key`, `message`, `details`, request id, `Retry-After`). Branch on the type or on the `key`, never on the message.

| Status | Go (`Error.Kind`) | TypeScript | Python | PHP (`Payrudder\Exception\…`) |
|---|---|---|---|---|
| 401 | `KindAuthentication` | `AuthenticationError` | `AuthenticationError` | `AuthenticationException` |
| 403 | `KindPermission` | `PermissionError` | `PermissionDeniedError` | `PermissionException` |
| 404 | `KindNotFound` | `NotFoundError` | `NotFoundError` | `NotFoundException` |
| 400, 422 | `KindValidation` | `ValidationError` | `ValidationError` | `ValidationException` |
| 409 | `KindConflict` | `ConflictError` | `ConflictError` | `ConflictException` |
| 429 | `KindRateLimit` | `RateLimitError` | `RateLimitError` | `RateLimitException` |
| 5xx | `KindServer` | `ServerError` | `ServerError` | `ServerException` |
| no answer (DNS, refused, reset, timeout) | `KindConnection` | `ConnectionError`, `TimeoutError` | `APIConnectionError`, `APITimeoutError` | `ConnectionException`, `TimeoutException` |
| any other | `KindAPI` | `PayrudderError` | `APIError` | `ApiException` |

A rejected usage event is **not** an error of the call: it is a result with `status: "rejected"` and an `error_code`, in all four SDKs.

The [command line](cli.html) prints an API error as `payrudder: 404 customer.not_found: … (request <id>)` and exits 1; a rejected event prints its `error_code` and exits 1; a command line it cannot understand exits 2 without sending anything. When you ask for help, quote the request id.

## The catalogue

The list below is generated from the code (`docs/error-keys.md`, written by the test of `pkg/errkey` when `UPDATE_ERROR_KEYS=1` is set, and checked by it otherwise) and so is always complete. It leaves out the keys of the operator's and the console's own routes (`admin`, `identity`, `support`), which an integration of the tenant API never receives.
