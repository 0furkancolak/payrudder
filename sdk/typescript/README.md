# Payrudder TypeScript / JavaScript SDK

Client for the tenant API of Payrudder (`/v1/*`, authenticated with an API key) and webhook signature verification.

> **Pre-release (`0.0.0`).** Nothing is published and no version is tagged until the whole platform is finished. The package is marked `private`. Until then, use it from a checkout of this repository.

- Node 20+, browsers and edge runtimes (it only needs the global `fetch`, `crypto.subtle`, `AbortController` and `FormData`).
- ESM and CommonJS builds with type declarations. `strict` TypeScript.
- No runtime dependencies.
- Licence: see the repository's root [`LICENSE`](../../LICENSE).

## Install (from the repository)

```sh
cd sdk/typescript
npm ci
npm run build          # writes dist/ (ESM + CJS + .d.ts)
npm pack               # optional: a tarball to install in another project
```

## Quickstart

```ts
import { Payrudder } from "@payrudder/sdk";

const payrudder = new Payrudder({
  apiKey: process.env.PAYRUDDER_API_KEY, // qk_test_... (sandbox) or qk_live_...
  baseUrl: "http://localhost:42800", // default; PAYRUDDER_BASE_URL is read when not given
});

await payrudder.customers.createCustomer({ external_id: "cus_42", name: "Ada", email: "ada@example.com" });

const results = await payrudder.events.ingest([
  { customer_external_id: "cus_42", meter_code: "ai_tokens", quantity: "1500", event_time: new Date() },
]);

const usage = await payrudder.customers.getCustomerUsage("cus_42", { period: "2026-11" });
```

Every operation of `api/openapi/v1.yaml` under `/v1/*` is one method, named exactly like its `operationId`, on the namespace of the first path segment (`/v1/credit-notes/...` is `payrudder.creditNotes`, `/v1/e-invoices/...` is `payrudder.eInvoices`). Arguments are, in order: the path parameters, the request body, the query object, then call options.

```ts
payrudder.subscriptions.cancelSubscription(id, { credit_unused: true });
payrudder.customers.setCustomerAddress("cus_42", { country: "TR", city: "Istanbul" });
payrudder.invoices.listInvoices({ customer: "cus_42", status: "paid" });
```

Request and response models are exported as types (`Customer`, `Invoice`, `CreateCustomerRequest`, ...). Field names stay snake_case, as on the wire. The spec marks most response fields as optional, so response properties are optional in the types.

### Client options

| Option | Default | |
|---|---|---|
| `apiKey` | `PAYRUDDER_API_KEY` | required |
| `baseUrl` | `PAYRUDDER_BASE_URL`, else `http://localhost:42800` | |
| `timeout` | `30000` | milliseconds, per attempt |
| `maxRetries` | `2` | automatic retries after the first attempt |
| `fetch` | global `fetch` | inject your own (tests, proxies) |
| `userAgent` | `payrudder-typescript/0.0.0` | browsers ignore it |

Per call, the last argument accepts `{ idempotencyKey, timeout, maxRetries, signal, headers }`.

## Money and decimals

`*_minor` amounts are int64 and travel as JSON **strings**. The SDK never turns them into a `number`:

- responses give you the string (`invoice.total_minor === "1116"`), use `toBigInt()` to compute with it;
- requests accept a string or a `bigint` (`amount_minor: 1000n`).

Decimal quantities and prices are strings too (`quantity: "1.250"`).

## Ingesting usage events

`events.ingest(events)` is the helper for `POST /v1/events`. It

- splits the input into valid batches (at most 500 events and 1 MiB each);
- fills a generated `idempotency_key` for events that have none (live keys require one). The key is chosen before the first attempt, so the automatic retries of a call can never double-count;
- returns one result per event, in input order. A rejected event is a result (`status: "rejected"`, `error_code`), not an exception. A failure of a whole batch (authentication, quota, network after the retries) throws.

```ts
const results = await payrudder.events.ingest(events);
for (const [i, r] of results.entries()) {
  if (r.status === "rejected") console.warn(events[i], r.error_code, r.error_message);
}
```

If you need to re-submit an input after a crash, give your own `idempotency_key`s: a repeat then comes back as `duplicate` and is not counted twice. The raw one-request operation is still available as `events.ingestEvents({ events })`.

## Errors

Every failed call throws a `PayrudderError` built from the API's `Error` envelope:

```ts
import { PayrudderError, NotFoundError, RateLimitError } from "@payrudder/sdk";

try {
  await payrudder.invoices.getInvoice(id);
} catch (err) {
  if (err instanceof NotFoundError) {
    /* 404 */
  } else if (err instanceof PayrudderError) {
    console.error(err.status, err.code, err.key, err.message, err.requestId, err.details);
  } else throw err;
}
```

| Class | When |
|---|---|
| `AuthenticationError` | 401 |
| `PermissionError` | 403 (missing permission, inactive tenant, sandbox-only route with a live key) |
| `NotFoundError` | 404 |
| `ConflictError` | 409 |
| `ValidationError` | 400, 422 |
| `RateLimitError` | 429 (`retryAfter` in seconds) |
| `ServerError` | 5xx |
| `ConnectionError` / `TimeoutError` | no usable response (`status` 0) |

`key` is the stable, translatable error key (`billing.credit_note.nothing_to_refund`): translate the key, not the message.

## Retries and idempotency

Retried automatically, with exponential backoff and jitter (capped): network errors and timeouts, `429` (the `Retry-After` header is honoured), `502`/`503`/`504`, and `500` only for idempotent operations: `GET`/`PUT`/`DELETE`, and `POST`s that carry an idempotency key. Other 4xx are never retried.

For operations that take an `Idempotency-Key` header (payment links, upload sessions) the SDK sends a generated key, the same one on every retry. For any other `POST`, pass `{ idempotencyKey }` in the call options to make it safe to retry after a `500` and safe for you to repeat after a crash:

```ts
await payrudder.customers.createCustomer(body, { idempotencyKey: "signup-8841" });
```

## Pagination

List operations that return a `next_page_token` have an async iterator under `payrudder.paginate` with the same arguments, yielding **items** across all pages:

```ts
for await (const customer of payrudder.paginate.listCustomers({ page_size: 200 })) {
  console.log(customer.external_id);
}
```

The plain `payrudder.customers.listCustomers({ page_token })` returns a single page.

## Webhooks

Verification uses WebCrypto (`crypto.subtle`), so it is **async** and runs unchanged in Node, browsers and edge runtimes. Pass the **raw** request body: a re-serialised JSON body will not verify.

```ts
import { constructEvent, WebhookEventTypes, WebhookVerificationError } from "@payrudder/sdk";

// Express: app.post("/webhooks/payrudder", express.raw({ type: "application/json" }), handler)
async function handler(req, res) {
  try {
    const event = await constructEvent(req.body, req.header("Payrudder-Signature") ?? "", process.env.PAYRUDDER_WEBHOOK_SECRET!);
    if (event.type === WebhookEventTypes.InvoicePaid) {
      console.log(event.data.invoice_id, event.data.net_minor);
    }
    res.sendStatus(200);
  } catch (err) {
    if (err instanceof WebhookVerificationError) return res.sendStatus(400); // err.reason says why
    throw err;
  }
}
```

```ts
// Cloudflare Workers / any fetch-style runtime
export default {
  async fetch(request: Request, env: { WEBHOOK_SECRET: string }) {
    const event = await constructEvent(await request.arrayBuffer(), request.headers.get("Payrudder-Signature") ?? "", env.WEBHOOK_SECRET);
    return new Response(event.id);
  },
};
```

- `verifyWebhook(payload, header, secret, tolerance = 300)` resolves or throws `WebhookVerificationError` (`reason`: `invalid_header`, `timestamp_outside_tolerance`, `no_matching_signature`). It accepts a string, `Uint8Array` or `ArrayBuffer`.
- `constructEvent(...)` verifies, then parses into a typed `WebhookEvent` you can narrow on `type`.
- During a secret rotation the header carries two `v1` signatures; either one matching is enough.
- `WebhookEventTypes` lists the event type names (`invoice.paid`, `subscription.cancelled`, `credits.low`, ... and `ping`).
- Events are delivered at least once and not in order: dedupe on `event.id`.

## Development

```sh
npm ci
npm run generate        # regenerate src/generated/* from ../../api/openapi/v1.yaml
npm run check           # generate:check, typecheck, lint, tests, build, ESM+CJS smoke
```

- `src/generated/` is produced by `scripts/generate.mjs` from `api/openapi/v1.yaml` and is checked in. A test fails when the checked-in code is stale, when an `operationId` under `/v1/*` has no client method, or when a client method is not an `operationId` (the only hand-written helper is `events.ingest`).
- Tests run against a local fake HTTP server; no network and no Payrudder stack are needed. An optional integration smoke test runs only when `PAYRUDDER_API_KEY` is set (and `PAYRUDDER_BASE_URL` if the gateway is not on `localhost:42800`).
