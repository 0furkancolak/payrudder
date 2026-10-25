# gateway

## Responsibility

The single public entry point. It terminates HTTP/JSON, authenticates API keys, resolves the tenant,
enforces per-tenant rate limits and quotas, and translates requests into gRPC calls to core services.
It holds **no business logic** and **no database**.

## Public HTTP API (v1)

All routes require `Authorization: Bearer qk_{live|test}_<prefix>_<secret>`. A `qk_test_` key resolves to the account's
**sandbox** tenant, a `qk_live_` key to its live tenant ([ADR 0011](../adr/0011-sandbox-environment.md)). JSON bodies, UTF-8.

| Method & path | Maps to | Rate class |
|---|---|---|
| `POST /v1/events` | `ingest.v1.IngestService/IngestEvents` | `ingest` |
| `GET /v1/customers/{external_id}/entitlements/{meter_code}` | `limits.v1.LimitsService/CheckEntitlement` | `read_hot` |
| `POST /v1/customers` · `GET /v1/customers` · `GET /v1/customers/{external_id}` | `plans.v1.PlansService/CreateCustomer`, `ListCustomers`, `GetCustomer` | `write` / `read` |
| `PUT /v1/customers/{external_id}/payment-method` | `SetPaymentMethod` | `write` |
| `POST /v1/meters` · `GET /v1/meters` · `GET /v1/meters/{code}` | `CreateMeter`, `ListMeters`, `GetMeter` | `write` / `read` |
| `POST /v1/plans` · `GET /v1/plans` · `POST /v1/plans/{code}/versions` · `GET /v1/plans/{code}/versions/{version\|latest}` | `CreatePlan`, `ListPlans`, `PublishPlanVersion`, `GetPlanVersion` | `write` / `read` |
| `POST /v1/subscriptions` · `DELETE /v1/subscriptions/{id}?end_time=` | `CreateSubscription`, `CancelSubscription` | `write` |
| `GET /v1/customers/{external_id}/usage?period=2026-11` | `aggregator.v1.UsageQueryService/GetCustomerUsage` | `read` |
| `GET /v1/invoices` · `GET /v1/invoices/{id}` · `GET /v1/invoices/{id}/pdf` | `billing.v1.BillingService/*` | `read` |
| `GET /v1/analytics/usage?…` | `analytics.v1.AnalyticsService/QueryUsage` | `analytics` |
| `POST /v1/assets` (`multipart/form-data`, ≤ 20 files, streamed) | `assets.v1.AssetsService/UploadAsset` (one client stream per file) | `write` (per file) |
| `POST /v1/assets/uploads` · `POST /v1/assets/uploads/{session}/files/{asset}/complete` · `…/parts` · `DELETE /v1/assets/uploads/{session}` | `CreateUploadSession`, `CompleteFile`, `PresignParts`, `AbortUpload` (direct multipart to S3) | `write` |
| `GET /v1/assets` · `GET /v1/assets/{id}` · `GET /v1/assets/{id}/download` · `DELETE /v1/assets/{id}` | `ListAssets`, `GetAsset`, `GetDownloadUrl`, `DeleteAsset` | `read` / `write` |
| `POST /v1/sandbox/periods/{period}/close` | `billing.v1.SandboxService/ClosePeriodNow` (sandbox keys only, else `403`) | `write` |
| `POST /v1/sandbox/customers/{external_id}/payment-scenario` `{"scenario":"declined"}` | `plans.v1.PlansService/SetPaymentMethod` with `pm_sandbox_<scenario>` (sandbox keys only, else `403`) | `write` |
| `POST /admin/v1/accounts` · `GET`/`PATCH /admin/v1/tenants/{id}` · `POST /admin/v1/tenants/{id}/api-keys` · `DELETE /admin/v1/api-keys/{id}` | `plans.v1.AdminService/*` | operator access only: the machine token, or the session of a platform operator ([phase 42](../phases/phase-42-signin-methods.md)) |

**Implemented in Phase 01:** the plans, sandbox payment-scenario and admin routes. The machine-readable contract is
[`api/openapi/v1.yaml`](../../api/openapi/v1.yaml); a contract test fails when it and the route table disagree.

JSON shapes follow the protobuf JSON mapping (`protojson`, snake_case, enums as strings such as `AGGREGATION_SUM`).
`int64` fields (`*_minor`) are rendered as JSON strings and accepted as strings or numbers. Single-resource responses return
the resource itself (`201` + `Customer`), list responses an object (`{"customers": [...], "next_page_token": "..."}`).
Error envelope: `{"error": {"code": "INVALID_ARGUMENT", "message": "...", "details": [{"field": "...", "description": "..."}], "request_id": "..."}}`.
gRPC → HTTP: `INVALID_ARGUMENT` 400, `UNAUTHENTICATED` 401, `PERMISSION_DENIED` 403, `NOT_FOUND` 404,
`ALREADY_EXISTS`/`ABORTED`/`FAILED_PRECONDITION` 409, body over 1 MiB 413 (`REQUEST_TOO_LARGE`), `RESOURCE_EXHAUSTED` 429,
`UNAVAILABLE` 503, `DEADLINE_EXCEEDED` 504. `INTERNAL` messages are never passed through.

`POST /v1/events` request sketch:

```json
{
  "events": [
    {
      "idempotency_key": "req_8f2c…",
      "customer_external_id": "cus_42",
      "meter_code": "ai_tokens",
      "quantity": "1500",
      "event_time": "2026-11-03T10:15:00Z",
      "properties": { "model": "gpt-x", "region": "eu" }
    }
  ]
}
```

`quantity` may be a JSON string or number. Either way its text is kept as written (the field is a `json.RawMessage`, so a number never becomes a `float64`) and ingest-svc parses it as an exact decimal
(`"1.25"` for a meter with scale ≥ 2). The response is always `202` for a structurally valid batch, with a per-event result
(`accepted` | `duplicate` | `rejected` + `error_code`). Whole-batch errors: `400` (malformed),
`401`, `413` (> 500 events or > 1 MiB; `POST /v1/assets` has its own limits, see [assets-svc.md](assets-svc.md#b-proxied-multipartform-data)), `429` (with `Retry-After`), `503`.

### Why a hand-written HTTP layer (not grpc-gateway)

Decided: `net/http` with Go 1.22+ `ServeMux` patterns, one handler per route that maps JSON to the gRPC client.
- The public API is small (~20 routes) and has shapes grpc-gateway handles awkwardly: per-event partial success in a `202`,
  `Retry-After` and rate-limit headers, exact decimal decoding of `quantity`, and one stable error envelope.
- Rate limiting must charge **per event** after decoding, so it lives in the handler anyway.
- The cost is a hand-maintained OpenAPI spec (`api/openapi/v1.yaml`), checked in CI by a contract test that calls every documented route.

## Middleware chain

`recover → otel (server span, extract traceparent) → request-id → body limit → auth → tenant ctx → rate limit → route → grpc client (inject metadata)`

## Authentication & tenant resolution

1. Parse key `qk_{env}_{prefix}_{secret}`. `prefix` is 8 chars and indexed. `secret` is 32 random bytes, base62.
2. Look up `gw:apikey:{prefix}` in Redis → `{tenant_id, environment, timezone, key_hash, status, tier}`.
   On a miss, call `plans.v1.AdminService/ResolveApiKey(prefix)` and cache for 60 s.
3. Constant-time compare `sha256(secret)` with `key_hash`. SHA-256 is enough because secrets are
   high-entropy random values, not passwords (see [06-multi-tenancy.md](../06-multi-tenancy.md#api-keys)).
4. Revocation: plans-svc deletes the cache key on revoke, so revocation takes effect immediately.
   The 60 s TTL only bounds how stale the cache can get if the delete fails.
5. Set gRPC metadata `x-quotify-tenant-id`, `x-quotify-environment`, `x-quotify-timezone`, `x-quotify-api-key-id`, `x-request-id`. Downstream services
   trust this metadata only from the gateway (network policy in K8s. mTLS is out of scope).

## Rate limiting

- Algorithm: **GCRA** (generic cell rate algorithm) in one Redis Lua script, key
  `gw:rl:{tenant_id}:{rate_class}`. It's O(1), stores one timestamp per key, and never sleeps.
- Limits come from the tenant **tier** (`free`, `standard`, `enterprise`) in the API key cache entry:

  | Tier | `ingest` events/s (burst) | `read_hot` req/s (burst) | `read` req/s (burst) | `write` req/s (burst) | `analytics` req/s (burst) |
  |---|---|---|---|---|---|
  | free | 100 (500) | 50 (100) | 20 (40) | 5 (10) | 0.5 (5) |
  | standard | 2,000 (10,000) | 500 (1,000) | 200 (400) | 50 (100) | 5 (20) |
  | enterprise | 10,000 (50,000) | 2,000 (4,000) | 800 (1,600) | 200 (400) | 20 (50) |

  The same GCRA step runs as a Lua script in Redis (using Redis `TIME`, so replicas share one clock) and in Go for the
  fallback. An integration test drives both with an identical sequence and asserts identical decisions.

- `ingest` is charged **per event**, not per request, so batching gives no bypass.
- If Redis is unavailable, **fail open** with a local in-memory token bucket per pod
  (limit ÷ replica count). Availability of ingest matters more than strict rate limits.
  Counted in metric `gateway_ratelimit_fallback_total`.

## Owned data

Redis only: `gw:apikey:{prefix}` (cache), `gw:rl:{tenant_id}:{class}` (GCRA state). Nothing durable.

## Consumed / produced messages

None.

## Failure modes

| Failure | Behaviour |
|---|---|
| plans-svc down | Cached keys keep working for ≤ 60 s. New or expired keys get `503` |
| ingest-svc down / slow | gRPC deadline 5 s (the default client timeout) → `503` + `Retry-After: 1`. Idempotency keys make client retries safe |
| Redis down | API-key cache misses go to plans-svc. Rate limiting falls back to a local bucket |
| Client sends same batch twice | Each event reports `duplicate` the second time. No double count |

## Scaling notes

CPU-bound on JSON decoding and TLS. Scale horizontally on CPU (HPA at 70%).
Keep gRPC client connections pooled. Use one `grpc.ClientConn` per upstream with round-robin load balancing over headless Services.


## As built: scopes, suspension and go-live (Phase 45)

The key cache entry carries the tenant status, the key scope and the end time of a rotated key. `Authenticate` refuses a prefix that disagrees with the tenant's environment (`401`), a key past its `expire_time` (`401`), and a genuine key of a tenant that is not active (`403 gateway.auth.tenant_not_active`). A key of scope `ingest` may only `POST /v1/events`, one of scope `read` only `GET`/`HEAD`; others get `403 gateway.auth.scope_denied`. Use is reported to plans-svc (`TouchApiKey`) at most once per key and replica every five minutes, in the background, with a bounded map. Routes: `GET`/`POST /app/v1/apps/{id}/go-live` (the gateway asks payments-worker `GET /admin/tenants/{id}/go-live` and passes the answer as `provider_ready`; a body cannot claim it), `POST /app/v1/apps/{id}/api-keys/{key_id}/rotate`, `scope` on key creation, and the operator's `PUT /admin/v1/tenants/{id}/status`.

## As built: sessions and the product API (Phase 38)

People sign in with `POST /auth/signup` / `/auth/signin` (public; 10 sign-ins and 5 sign-ups a minute per client address; `QUOTIFY_TRUST_FORWARDED_FOR=true` makes the limit count by `X-Forwarded-For`, believed only from a private or loopback peer) and use `Authorization: Bearer qs_…` on `Access = AccessSession` routes: `/auth/signout`, `/auth/me`, `/app/v1/account`, `/app/v1/apps[/{id}]`, `/app/v1/apps/{id}/overview`, `/app/v1/apps/{id}/dashboard`, `/app/v1/apps/{id}/api-keys[/{key_id}]`. Each request resolves the token with `IdentityService.GetSession` (no session cache: sign-out is immediate) and is rate limited per user (`user:<id>`, standard tier). A session may also call `/v1/*` with `X-Quotify-Tenant` for a tenant of one of its accounts (`ResolveMyTenant`; a viewer may only GET); it is never an operator. The overview composes payments-worker's ledger, billing, plans and webhooks as the environment's tenant. See [phase 38](../phases/phase-38-product-app.md).

**Phase 39:** `/auth/signup` answers `202` and opens no session; public `POST /auth/verify`, `/auth/forgot` (5 a minute per client address) and `/auth/reset` (20 a minute), and `POST /auth/resend-verification` for a session. Members: `PATCH /app/v1/account`, `GET|POST /app/v1/account/invitations`, `DELETE /app/v1/account/invitations/{id}`, `POST …/{id}/resend`, `PATCH|DELETE /app/v1/account/members/{user_id}`, `POST /app/v1/invitations/accept` (session), and the public `POST /auth/invitations/lookup` and `/auth/invitations/accept` (20 a minute per client address; the token is in the body). See [phase 39](../phases/phase-39-product-app-complete.md).

**Phase 42:** the three ways to sign in. `GET /auth/methods` (which are on), `POST /auth/otp/request` (5 a minute per client address, always `202`) and `POST /auth/otp/verify` (10 a minute), `GET /auth/google/start` and `/auth/google/callback` (20 a minute; 404 unless `QUOTIFY_GOOGLE_CLIENT_ID` and `_SECRET` are set; the callback verifies the ID token and hands the session to the console in a URL fragment). The sign-in, code and `/auth/me` answers carry `operator: true|false`, the server's verdict: the user's address is on `QUOTIFY_OPERATOR_EMAILS` and verified. `operatorAllowed` lets an operator's **session** through the operator routes (and act as any tenant) in addition to the machine token `QUOTIFY_OPERATOR_TOKEN`; a valid session of anybody else is `403 identity.operator.forbidden`, an invalid one `401`. Details: [phase 42](../phases/phase-42-signin-methods.md).
Billing pages: `GET /v1/payments` (tenant route; the ledger folded to payments, newest first, from payments-worker's internal ledger of the principal's own tenant) joins the invoice, plan, coupon, tax, dunning and report reads that the product app's pages call as a session acting on its own tenant (viewers GET only); the operator's ledger, reconciliation and invoice saga stay under `/admin/v1`.

**Phase 41:** `GET /app/v1/apps/{id}/dashboard` (and the operator's `GET /admin/v1/tenants/{tenant_id}/dashboard`) composes a period with the one before it, one block per currency, from billing's accounting summary and `GetDashboardStats`, plans' subscription report (by plan) and customer stats, the payment ledger and limits; sections fail independently (`unavailable`), a complete answer is cached for 15 seconds. See [phase 41](../phases/phase-41-ui-overhaul.md).
