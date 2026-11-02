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

An `INTERNAL` error never carries its cause in the message. A request that timed out may or may not have been done: retry it only with an idempotency key, or read the state first. The SDKs retry `429` and `5xx` for you with the same key.

A usage event is judged on its own inside a `202` answer: an event that cannot be counted is `rejected` with an `error_code` (the [list is below](#event-codes)) and does not fail the others. See [Usage metering](usage-metering.html).

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

The list below is generated from the code (`docs/error-keys.md`, written by the test of `pkg/errkey`) and so is always complete. It leaves out the keys of the operator's and the console's own routes (`admin`, `identity`, `support`), which an integration of the tenant API never receives.
