# Payrudder Go SDK

Go client for the Payrudder tenant API (`/v1/*`, authenticated with an API key) and a verifier for webhook deliveries.
Standard library only; Go 1.23 or newer.

> **Pre-release.** The version is `0.0.0` and nothing is published or tagged: Payrudder is released as a whole, and
> until then the API of this package can change. Use it from a clone of the repository (see Install).

It covers the tenant API only: not `/admin/v1` (operator) and not `/app/v1` or `/auth` (console sessions).

## Install

Not on a module proxy yet. From a checkout of this repository, in your own module:

```sh
go mod edit -require=github.com/0furkancolak/payrudder/sdk/go@v0.0.0
go mod edit -replace=github.com/0furkancolak/payrudder/sdk/go=/path/to/payrudder/sdk/go
```

or add `./sdk/go` to your `go.work`. The import path is `github.com/0furkancolak/payrudder/sdk/go`; the package is named
`payrudder`.

## Quickstart

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

func main() {
	// WithAPIKey and WithBaseURL default to $PAYRUDDER_API_KEY and $PAYRUDDER_BASE_URL
	// (then http://localhost:42800). A qk_test_ key acts on the sandbox, a qk_live_ key on the live tenant.
	client, err := payrudder.New(payrudder.WithAPIKey("qk_test_..."))
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	cust, err := client.Customers.CreateCustomer(ctx, &payrudder.CreateCustomerRequest{
		ExternalID: "cus_42", Name: "Acme Ltd", Email: "billing@acme.example",
	})
	if err != nil {
		log.Fatal(err)
	}

	results, err := client.Events.Ingest(ctx, []payrudder.IncomingEvent{{
		CustomerExternalID: cust.ExternalID,
		MeterCode:          "ai_tokens",
		Quantity:           "1500",
		EventTime:          time.Now(),
		Properties:         map[string]string{"model": "gpt-x"},
	}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(results[0].Status) // accepted | duplicate | rejected
}
```

### Client options

| Option | Default |
|---|---|
| `WithAPIKey(key)` | `$PAYRUDDER_API_KEY`, required |
| `WithBaseURL(url)` | `$PAYRUDDER_BASE_URL`, else `http://localhost:42800` |
| `WithTimeout(d)` | 30 s per attempt |
| `WithMaxRetries(n)` | 2 |
| `WithRetryBackoff(base, max)` | 500 ms, 8 s |
| `WithTransport(rt)` | `http.DefaultTransport`; any `http.RoundTripper`, handy in tests |
| `WithUserAgent(ua)` | `payrudder-go/0.0.0` |

A `Client` is safe for concurrent use. Redirects are never followed, so the key cannot leak to another host.

### Methods

Each operation of `api/openapi/v1.yaml` under `/v1` is one method on a namespace named after the first path segment:
`client.Customers`, `client.Events`, `client.Invoices`, `client.Subscriptions`, `client.WebhookEndpoints`, ... The method
name is the `operationId` in PascalCase (`createCustomer` becomes `Customers.CreateCustomer`). Arguments are always

```
(ctx, path parameters..., request body, query parameters, ...RequestOption)
```

with the body and the query struct omitted when the operation has none, and both may be `nil`. Responses are typed
structs; CSV and file downloads return `[]byte`; operations without a body return only `error`.

Per call you can add `WithIdempotencyKey`, `WithRequestHeader`, `WithRequestTimeout` and `WithRequestMaxRetries`.

### Money, decimals and optional fields

- `*_minor` amounts and every `int64` travel as JSON **strings**. They are the type `payrudder.Int64`
  (`type Int64 int64`), read from a string or a number and written as a string, so no amount ever passes through a float.
  Convert with `int64(x)`.
- Usage quantities are `payrudder.Decimal` (a string such as `"1.250"`); the text is sent exactly as given.
- In request structs, optional numbers, booleans, times and objects are pointers so that `0` and `false` can be sent;
  use `payrudder.Ptr(25)`. Optional strings are plain strings and are left out when empty. Response structs use plain
  values; a field the server leaves out is the zero value (nullable fields are pointers).
- Enumerations are plain strings; the allowed values are listed in the field comments.

## Errors

Every failure is a `*payrudder.Error`, found with `errors.As`:

```go
cust, err := client.Customers.GetCustomer(ctx, "cus_404")
var perr *payrudder.Error
if errors.As(err, &perr) {
	switch perr.Kind {
	case payrudder.KindNotFound:
		// no such customer
	case payrudder.KindRateLimit:
		time.Sleep(perr.RetryAfter) // the client already retried; this is what is left
	default:
		log.Printf("%v (status %d, key %q, request %s)", err, perr.Status, perr.Key, perr.RequestID)
	}
}
// Or by sentinel: errors.Is(err, payrudder.ErrNotFound)
```

An `Error` carries `Status`, `Code` (`INVALID_ARGUMENT`), `Key` (the stable, translatable key such as
`billing.credit_note.nothing_to_refund`: branch on this, not on `Message`), `Message`, `Details` (offending fields),
`RequestID` (envelope or `X-Request-Id`) and `RetryAfter`. The kinds, also available as `errors.Is` sentinels, are:

| Kind | Sentinel | HTTP |
|---|---|---|
| `KindAuthentication` | `ErrAuthentication` | 401 |
| `KindPermission` | `ErrPermission` | 403 |
| `KindNotFound` | `ErrNotFound` | 404 |
| `KindValidation` | `ErrValidation` | 400, 422 |
| `KindConflict` | `ErrConflict` | 409 |
| `KindRateLimit` | `ErrRateLimit` | 429 |
| `KindServer` | `ErrServer` | 5xx |
| `KindConnection` | `ErrConnection` | no answer: network failure or timeout (`Timeout` is true for a deadline; the cause is wrapped) |
| `KindAPI` | | any other status, such as 413 |

## Retries and idempotency

A failed call is repeated up to `maxRetries` times (default 2) with exponential backoff and jitter (500 ms doubling to
8 s):

- network errors and timeouts, `429` (the `Retry-After` seconds are used; more than 60 s is not waited for and comes back
  in `Error.RetryAfter`), `502`, `503` and `504`: always;
- `500`: only for `GET`, `PUT`, `DELETE`, and for a `POST` that carries an idempotency key;
- every other `4xx`: never.

The body and the `Idempotency-Key` are the same in every attempt. Where the API supports the header (payment links, upload
sessions), pass `payrudder.WithIdempotencyKey("...")`. Network errors are retried for every method, so give a `POST`
that must not run twice an idempotency key.

## Ingesting events

```go
results, err := client.Events.Ingest(ctx, events) // any number of events
```

`Ingest` splits the input into valid batches (at most 500 events and 1 MiB each), gives every event without an
`IdempotencyKey` a generated one (live keys require it; it is fixed before the first attempt, so retries cannot double
count), and returns one `EventResult` per input event, **in input order**. A rejected event is a result with
`Status == "rejected"` and an `ErrorCode`, not an error. If a whole batch fails, the results of the batches before it are
returned together with the error. An event that alone exceeds 1 MiB is reported as rejected with
`payrudder.ErrCodeEventTooLarge` without being sent. The caller's slice is not modified. `Events.IngestEvents` sends one
raw batch.

## Pagination

List operations that take `page_token` have an iterator next to the single-page call, named with the suffix `All`
(Go 1.23 range-over-func):

```go
for cust, err := range client.Customers.ListCustomersAll(ctx, &payrudder.ListCustomersParams{PageSize: payrudder.Ptr(100)}) {
	if err != nil {
		return err // yielded once, ends the loop
	}
	fmt.Println(cust.ExternalID)
}
```

`break` stops fetching. Page by hand with `ListCustomers` and `NextPageToken` when you need the cursor.

## Webhooks

```go
func handler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)) // the raw bytes, not re-encoded JSON
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ev, err := payrudder.ConstructEvent(body, r.Header.Get(payrudder.WebhookSignatureHeader), os.Getenv("PAYRUDDER_WEBHOOK_SECRET"), 0)
	if err != nil {
		http.Error(w, "bad signature", http.StatusBadRequest) // errors.Is(err, payrudder.ErrWebhookSignature | ErrWebhookTimestamp | ErrWebhookHeader)
		return
	}
	switch ev.Type {
	case payrudder.EventInvoicePaid:
		var d payrudder.InvoiceEventData
		if err := ev.DecodeData(&d); err != nil { /* ... */ }
		// d.TotalMinor is an Int64; deliveries repeat, so deduplicate on ev.ID
	}
	w.WriteHeader(http.StatusNoContent)
}
```

`VerifyWebhook(payload, header, secret, tolerance)` implements the scheme of `docs/16-webhooks.md`: HMAC-SHA256 over
`"<t>.<raw body>"`, constant-time comparison, any of several `v1=` signatures (a rotated secret keeps signing for 24 hours),
timestamps more than `tolerance` from the clock refused (`0` means five minutes). `WebhookEventTypes` and the `Event*`
constants list the event type names; `InvoiceEventData`, `CreditNoteEventData`, `SubscriptionEventData`,
`PaymentLinkEventData` and `CreditsEventData` type the payloads. Answer `2xx` within 10 seconds.

## Uploads

`Assets.UploadAssets(ctx, []payrudder.AssetFile{...})` builds the multipart request (up to 20 files, 25 MiB each). Larger
files use `CreateUploadSession`, `PresignParts` and `CompleteUploadFile`.

## Development

The typed part (`types_gen.go`, `services_gen.go`) is generated from `api/openapi/v1.yaml`:

```sh
cd sdk/go && go generate ./...
```

The generator and the contract test live in `internal/contract`, a nested module (it needs a YAML parser; the SDK has no
dependencies). The test fails when an `operationId` under `/v1` has no client method, when a method has no operation,
or when the generated files are stale. Hand-written methods are `Events.IngestEvents`, `Events.Ingest` and
`Assets.UploadAssets`.

```sh
cd sdk/go
GOWORK=off go vet ./... && GOWORK=off go test -race ./...
(cd internal/contract && GOWORK=off go test -race ./...)
PAYRUDDER_API_KEY=qk_test_... go test -run Integration ./...   # optional smoke test against a running stack
```

The unit tests run against a local fake server; no network and no Payrudder stack are needed.

## License

The same as the repository: see [`LICENSE`](LICENSE), which points to the root licence.
