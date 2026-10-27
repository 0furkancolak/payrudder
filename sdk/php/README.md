# Payrudder PHP SDK

PHP client for the tenant API of [Payrudder](../../README.md) (`/v1/*`, authenticated with an API key) and verification of webhook deliveries.

> **Pre-release.** The package version is `0.0.0`. Nothing is published to Packagist and no version is tagged until the whole app is finished.

- PHP 8.1+, 64-bit, `ext-json`. No other runtime package: the default transport is `ext-curl`.
- Typed readonly models and backed enums, `declare(strict_types=1)` everywhere, PHPStan level max.
- One method per operation of [`api/openapi/v1.yaml`](../../api/openapi/v1.yaml), grouped by the first path segment: `$client->customers->createCustomer(...)`.
- Retries, typed errors, generator based pagination, a batching ingest helper.

Licence: see [`LICENSE`](../../LICENSE) at the repository root (Elastic License 2.0).

## Install

Until the package is released, require it from a path or a VCS checkout of this repository:

```json
{
  "repositories": [{ "type": "path", "url": "../quotify/sdk/php" }],
  "require": { "payrudder/payrudder-php": "*@dev" }
}
```

## Quickstart

```php
use Payrudder\Client;

$client = new Client(apiKey: 'qk_test_...');   // or set PAYRUDDER_API_KEY
// baseUrl defaults to $PAYRUDDER_BASE_URL, then http://localhost:42800

$client->customers->createCustomer([
    'external_id' => 'cus_42',
    'name' => 'Acme',
    'email' => 'billing@acme.example',
]);

$result = $client->events->ingest([
    ['customer_external_id' => 'cus_42', 'meter_code' => 'ai_tokens', 'quantity' => '1500', 'event_time' => '2026-11-03T10:15:00Z'],
]);
var_dump($result[0]->status === \Payrudder\Enum\EventResultStatus::Accepted);   // accepted | duplicate | rejected

$entitlement = $client->customers->getEntitlement('cus_42', 'ai_tokens');
```

Request bodies are either an array in the wire format (snake_case) or the typed request class
(`new \Payrudder\Model\CreateCustomerRequest(externalId: 'cus_42', name: 'Acme', email: '...')`).
Responses are typed models with camelCase properties (`$customer->externalId`). Field names on the wire stay snake_case.

### Client options

| Argument | Default |
|---|---|
| `apiKey` | `$PAYRUDDER_API_KEY` (required) |
| `baseUrl` | `$PAYRUDDER_BASE_URL`, else `http://localhost:42800` |
| `timeout` | `30.0` seconds per attempt |
| `maxRetries` | `2` |
| `userAgent` | `payrudder-php/0.0.0` |
| `transport` | `TransportInterface`, replaces the whole HTTP layer (tests) |
| `httpClient`, `requestFactory`, `streamFactory` | a PSR-18 client and PSR-17 factories (see below) |
| `sleeper` | `Closure(float $seconds)`, how to wait between retries (tests) |

Per call, pass a `RequestOptions` as the last argument: `idempotencyKey`, `headers`, `timeout`, `maxRetries`.

### HTTP client

The transport is, in order: the `transport` you pass; a PSR-18 `httpClient` you pass (the PSR-17 factories are found with `php-http/discovery` when you do not pass them: install `php-http/discovery` and a PSR-17 implementation such as `nyholm/psr7`); `ext-curl`; a client found by `php-http/discovery`.
Redirects are never followed by the curl transport, so the API key cannot be sent to another host. PSR-18 has no per-request timeout: configure it on your client.

## Money and decimals

`*_minor` values (int64) travel as JSON strings. The models expose them as PHP `int` (64-bit PHP is required; the client refuses to start on 32-bit) and a value that does not fit is an `UnexpectedResponseException`, never a float. Requests send them as strings.
Decimal quantities and prices are strings and stay strings: pass `'1.250'`, not `1.25`.

```php
$invoice = $client->invoices->getInvoice($id);
$invoice->totalMinor;           // int, e.g. 1116
$invoice->lines[0]->quantity;   // string, e.g. "1.250"
```

Enum fields hold the backed enum case when this SDK knows the value, and the raw string when the server is newer than the SDK:

```php
if ($invoice->status === \Payrudder\Enum\InvoiceStatus::Paid) { ... }
```

## Errors

Every failure is a `Payrudder\Exception\PayrudderException`.

| Class | When |
|---|---|
| `AuthenticationException` | 401 |
| `PermissionException` | 403 |
| `NotFoundException` | 404 |
| `ValidationException` | 400, 422 |
| `ConflictException` | 409 |
| `RateLimitException` | 429 |
| `ServerException` | 5xx |
| `ApiException` | any other error status (base of the above) |
| `ConnectionException`, `TimeoutException` | no answer at all |

An `ApiException` carries the `Error` envelope: `status`, `errorCode` (`INVALID_ARGUMENT`), `errorKey` (the translatable key, e.g. `billing.credit_note.nothing_to_refund`), `getMessage()`, `details` (field errors), `requestId`, `retryAfter` and `responseBody`.

```php
try {
    $client->creditNotes->getCreditNote($id);
} catch (\Payrudder\Exception\NotFoundException $e) {
    error_log($e->errorKey . ' ' . $e->requestId);
}
```

## Retries and idempotency

Automatic retries, with exponential backoff and jitter (0.5 s, 1 s, 2 s, ... capped at 8 s):

- network errors and timeouts;
- 429, waiting for `Retry-After` (a delay above 60 s is raised at once instead of blocking);
- 502, 503, 504;
- 500, but only for GET/PUT/DELETE and for calls that carry an idempotency key.

Other 4xx are never retried. `maxRetries: 0` turns it off.

To make a mutating call safe to repeat, send an idempotency key; the same key is used by all automatic retries of that call:

```php
use Payrudder\RequestOptions;

$link = $client->paymentLinks->createPaymentLink(
    ['kind' => 'adhoc', 'amount_minor' => '4900', 'currency' => 'EUR'],
    new RequestOptions(idempotencyKey: 'order-1234'),
);
```

## Ingesting events

`events->ingest()` takes any number of events (arrays or `IncomingEvent` objects) and sends them in as many requests as the limits need (at most 500 events and 1 MiB each), in order. An event without an `idempotency_key` gets a generated one before the first attempt, so a retry can never count it twice (live keys require one). It returns one result per event, in request order, and does not throw for a rejected event.

```php
foreach ($client->events->ingest($events) as $i => $r) {
    if ($r->status === \Payrudder\Enum\EventResultStatus::Rejected) {
        error_log("event $i rejected: " . ($r->errorCode instanceof \BackedEnum ? $r->errorCode->value : $r->errorCode));
    }
}
```

A whole-batch error (401, 413, 429 after the retries, 503) throws. Batches sent before it stay sent; calling again with the same `idempotency_key`s is safe.
`events->ingestEvents($body)` is the raw single-request call.

## Pagination

Each list operation that returns a `next_page_token` has two generators next to the single-page call:

```php
$page = $client->customers->listCustomers(pageSize: 100);                 // one page

foreach ($client->customers->listCustomersIterator() as $customer) { }   // every item
foreach ($client->customers->listCustomersPages() as $page) { }          // every page
```

They are lazy: a request is made when the loop needs the next page. A `pageToken` passed in starts the walk there.

## Webhooks

Verify against the **raw** request body, byte for byte (`file_get_contents('php://input')`), not a re-encoded one:

```php
use Payrudder\Webhook\Webhook;
use Payrudder\Webhook\WebhookEventType;
use Payrudder\Exception\WebhookException;

$payload = file_get_contents('php://input');
$header  = $_SERVER['HTTP_PAYRUDDER_SIGNATURE'] ?? '';

try {
    $event = Webhook::constructEvent($payload, $header, $secret);   // tolerance 300 s
} catch (WebhookException $e) {
    http_response_code(400);
    exit;
}

if ($event->type === WebhookEventType::INVOICE_PAID) {
    $event->data['invoice_id'];
    $event->data['total_minor'];   // a string of minor units
}
http_response_code(200);
```

`Webhook::verify()` throws `WebhookException`, `Webhook::isValid()` returns a boolean. The scheme is documented in [`docs/16-webhooks.md`](../../docs/16-webhooks.md): HMAC-SHA256 over `"<t>.<body>"`, constant-time comparison, several `v1` signatures while a secret is being rotated, and a timestamp more than `tolerance` seconds from now is refused. Deliveries are at least once and not in order: use `$event->id` to drop a repeat.
`WebhookEventType::all()` lists the type names (and the `Payrudder\Enum\WebhookEventType` enum is what an endpoint's `event_types` accepts).

## Uploads

`$client->assets->uploadAssets([...paths or ['filename' => ..., 'content' => ..., 'content_type' => ...]], $metadata)` sends one multipart request (up to 20 files, 25 MiB each). For larger files use the upload-session operations (`createUploadSession`, `presignParts`, `completeUploadFile`).

## Development

```sh
composer update
composer test        # PHPUnit, against fakes and the PHP built-in server; no network
composer stan        # PHPStan, level max
composer cs          # php-cs-fixer, dry run  (composer cs:fix to apply)
composer generate    # regenerate src/Model, src/Enum and src/Resource/Generated from the spec
```

`src/Model`, `src/Enum` and `src/Resource/Generated` are generated by `tools/generate.php` from `api/openapi/v1.yaml`; CI fails when they are out of date. `tests/SpecSyncTest.php` fails when an `operationId` under `/v1` has no client method, or a method has no operation. The hand-written parts are the client, transport, retries, `Webhook/`, `Resource/EventsIngest.php` and `Resource/AssetsUploads.php`.

`tests/IntegrationTest.php` is a smoke test against a running stack; it is skipped unless `PAYRUDDER_API_KEY` (a `qk_test_` key) is set.
