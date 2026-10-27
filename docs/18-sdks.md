# 18 — SDKs

Client libraries for the **tenant API** (`/v1/*`, authenticated with an API key `qk_test_…` or `qk_live_…`) and for verifying webhook signatures, so other applications can meter usage, manage customers and subscriptions and react to events without writing HTTP by hand. The operator API (`/admin/v1`) and the console's session routes (`/app/v1`, `/auth`) are not part of them.

| Language | Directory | Package | Notes |
|---|---|---|---|
| TypeScript / JavaScript | [`sdk/typescript`](../sdk/typescript) | `@payrudder/sdk` | Node 20+, browsers and edge runtimes (global `fetch` and WebCrypto); ESM + CJS + `.d.ts` |
| Go | [`sdk/go`](../sdk/go) | `github.com/0furkancolak/payrudder/sdk/go` | its own module, standard library only; `iter.Seq2` pagination |
| Python | [`sdk/python`](../sdk/python) | `payrudder` | 3.10+, typed (`py.typed`), sync `Payrudder` and async `AsyncPayrudder` on `httpx` |
| PHP | [`sdk/php`](../sdk/php) | `payrudder/payrudder-php` | 8.1+, PSR-18 or the built-in curl transport; typed models and backed enums |

**Nothing is published and no version is tagged** until the whole platform is finished: every package says `0.0.0`. Use them from a checkout of this repository.

## One design, four languages

- **One method per operation.** Every `operationId` of [`api/openapi/v1.yaml`](../api/openapi/v1.yaml) under `/v1/*` is a method, named after the `operationId` in the language's usual case, on a resource namespace taken from the first path segment (`customers`, `invoices`, `creditNotes`, …). Arguments: path parameters, the body, the query, then per-call options (idempotency key, timeout, retries, headers).
- **Generated from the spec, checked against it.** Models and methods are generated from the OpenAPI file (`npm run generate`, `go generate ./...`, `python tools/generate.py`, `composer generate`); each SDK has a test that fails when an operation has no method (or a method no operation) and CI checks that the generated files are current.
- **Money is never a float.** `*_minor` amounts are int64 strings on the wire and a safe integer type in the SDK (TypeScript `string` or `bigint`, Go `payrudder.Int64`, Python `int`, PHP `int` that refuses to overflow); decimal quantities and prices are strings or `Decimal`.
- **Ingest helper.** `events.ingest` splits a list into batches of at most 500 events and 1 MiB, fills an idempotency key per event when the caller gives none (the key is stable across retries), and returns one result per event in request order; a rejected event is a result, not an exception.
- **Retries.** Network errors, `429` (honours `Retry-After`), `502`, `503` and `504` are retried with exponential backoff and jitter; a `500` only for idempotent calls or a POST that carries an idempotency key. No other `4xx` is retried.
- **Errors.** One typed error carrying the HTTP status, `code`, the translatable error `key` (for example `billing.credit_note.nothing_to_refund`, see [error keys](error-keys.md)), message, details, request id and `Retry-After`, with subtypes for authentication, permission, not found, validation, conflict, rate limit, server and connection or timeout.
- **Pagination.** Every list operation that has a `page_token` also has an iterator (generator, async iterator or `iter.Seq2`) that walks all pages.
- **Webhooks.** `verifyWebhook` / `constructEvent` implement the scheme of [16 — Webhooks](16-webhooks.md): HMAC-SHA256 over `t.payload`, constant-time comparison, several `v1` signatures for secret rotation, a 300 s tolerance. The event type names are constants.

## Tests

Unit tests run against a local fake HTTP server (retries, backoff, `Retry-After`, key stability, error mapping, pagination, ingest splitting, webhook verification with tampered payloads, stale timestamps and rotated secrets). An optional smoke test per SDK runs against a real stack when `PAYRUDDER_API_KEY` is set. CI: one workflow per SDK (`.github/workflows/sdk-<language>.yml`), triggered by changes to its directory or to the spec.

## Not done

- The generators emit the types reachable from `paths`; the per-event webhook `data` schemas of the spec (`WebhookEvent` is a discriminated `oneOf`) are not generated into the SDKs yet, so webhook payloads are typed by hand.
- Lists the backend does not page (meters, plans, coupons, webhook endpoints, expense rules, credit packs, entitlements) have no iterator.
- Other languages (Java, C#, Ruby) are not built; the OpenAPI file is the contract for them.
