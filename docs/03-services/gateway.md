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
| `POST /admin/v1/accounts` · `GET`/`PATCH /admin/v1/tenants/{id}` · `POST /admin/v1/tenants/{id}/api-keys` · `DELETE /admin/v1/api-keys/{id}` | `plans.v1.AdminService/*` | operator access only: the machine token, or the session of a platform operator (phase 42) |

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
5. Set gRPC metadata `x-payrudder-tenant-id`, `x-payrudder-environment`, `x-payrudder-timezone`, `x-payrudder-api-key-id`, `x-request-id`. Downstream services
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

Reference data in the binary: the GeoNames country, region and district lists (`internal/geo`, embedded gzip, made by `cmd/geoimport`), served read-only
under `GET /app/v1/geo/*` to any session. See [geo-data.md](../geo-data.md).

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

The key cache entry carries the tenant status, the key's role and permissions (an entry written before phase 53 has only a `scope`, which reads as the role of the same meaning; the role `admin` always holds every permission that exists) and the end time of a rotated key. `Authenticate` refuses a prefix that disagrees with the tenant's environment (`401`), a key past its `expire_time` (`401`), and a genuine key of a tenant that is not active (`403 gateway.auth.tenant_not_active`). Every tenant route needs one permission, worked out from its method and path by `pkg/apikey` `RequiredPermission` (`/v1/customers` is `customers:read` for `GET` and `customers:write` otherwise, `POST /v1/events` is `events:write`, `/v1/sandbox/*` is `sandbox:write`, ...); a key without it gets `403 gateway.auth.permission_denied` whose message names the permission. A caller that is not an API key (a session, the operator) carries no role and is not limited here. `TestEveryTenantRouteNeedsAKnownPermission` fails for a route nobody classified, and `TestOpenAPIKeyVocabularyMatchesTheCode` keeps the documented roles and permissions equal to the enforced ones. Use is reported to plans-svc (`TouchApiKey`) at most once per key and replica every five minutes, in the background, with a bounded map. Routes: `GET`/`POST /app/v1/apps/{id}/go-live` (the gateway asks payments-worker `GET /admin/tenants/{id}/go-live` and passes the answer as `provider_ready`; a body cannot claim it), the owner's own steps (`PUT /app/v1/apps/{id}/billing-profile`, `POST /app/v1/apps/{id}/go-live/terms`, and `GET`/`PUT`/`DELETE /app/v1/apps/{id}/go-live/providers[/{provider}]`: the tenant and the environment are the app's live ones whatever the request says, the provider must be one with an adapter (never the mock), the actor is the session's person, and the settings are forwarded to payments-worker and never echoed), `POST /app/v1/apps/{id}/api-keys/{key_id}/rotate`, `role` and `permissions` on key creation (the older `scope` of `full`, `ingest` or `read` still works when neither is given), and the operator's `PUT /admin/v1/tenants/{id}/status`. `POST /app/v1/apps/{id}/promote` (phase 45 D) copies the sandbox configuration to live (`dry_run`, `plan_hash`); the answer can hold a new webhook secret and is `no-store`. Sandbox (phase 45 E): `POST`/`GET /app/v1/apps/{id}/sandbox/reset` (queue and read the reset of the app's sandbox), `GET /v1/sandbox/scenarios` and `POST /v1/sandbox/invoices/{id}/simulate` (sandbox keys only).

## As built: sessions and the product API (Phase 38)

People sign in with `POST /auth/signup` / `/auth/signin` (public; 10 sign-ins and 5 sign-ups a minute per client address; `X-Forwarded-For` is believed, so the limit counts the visitor, only from a trusted proxy: a request carrying `X-Edge-Auth` equal to `PAYRUDDER_TRUSTED_PROXY_SECRET` (constant-time compare; the console's Worker sends it), a peer inside `PAYRUDDER_TRUSTED_PROXIES` (CIDRs, empty by default), or, with `PAYRUDDER_TRUST_FORWARDED_FOR=true`, a private or loopback peer; the same address is used for the public hosted-invoice limits) and use `Authorization: Bearer qs_…` on `Access = AccessSession` routes: `/auth/signout`, `/auth/me`, `/app/v1/account`, `/app/v1/apps[/{id}]` (`PATCH` renames for owners and admins, `DELETE` archives for owners only: see plans-svc, "Renaming and deleting an app"), `/app/v1/apps/{id}/overview`, `/app/v1/apps/{id}/dashboard`, `/app/v1/apps/{id}/api-keys[/{key_id}]`, `GET|PATCH /app/v1/me/onboarding` (the person's wizard progress and seen tours; no API key can call it, it has no person, so it needs no `RequiredPermission` class: that table covers the tenant routes only). Each request resolves the token with `IdentityService.GetSession` (no session cache: sign-out is immediate) and is rate limited per user (`user:<id>`, standard tier). A session may also call `/v1/*` with `X-Payrudder-Tenant` for a tenant of one of its accounts (`ResolveMyTenant`; a viewer may only GET); it is never an operator. The overview composes payments-worker's ledger, billing, plans and webhooks as the environment's tenant. See phase 38.

**Phase 39:** `/auth/signup` answers `202` and opens no session; public `POST /auth/verify`, `/auth/forgot` (5 a minute per client address) and `/auth/reset` (20 a minute), and `POST /auth/resend-verification` for a session. Members: `PATCH /app/v1/account`, `GET|POST /app/v1/account/invitations`, `DELETE /app/v1/account/invitations/{id}`, `POST …/{id}/resend`, `PATCH|DELETE /app/v1/account/members/{user_id}`, `POST /app/v1/invitations/accept` (session), and the public `POST /auth/invitations/lookup` and `/auth/invitations/accept` (20 a minute per client address; the token is in the body). See phase 39.

**Phase 46:** the promotion code. `/i/{token}` of an ad-hoc or credit-pack link has a *Promotion code* field; `POST /i/{token}/promo` (`code`) shows the price, the discount and the total to pay (or the bonus credits), or why the code cannot be used (`422`, in the page's language, from the `coupon.*` key); *Continue* (`POST /i/{token}/start`) carries the code and claims it. The credit packs of the portal (`/c/{token}`) have a field too (`POST /c/{token}/credits/buy` takes `code`). Both POSTs count against the 10 a minute per address, which also limits guessing codes. Operator API: `POST /v1/coupons/{code}/promotion-codes`, `GET /v1/promotion-codes`, `DELETE /v1/promotion-codes/{code}`, `GET /v1/coupon-redemptions`, `POST /v1/promotions/quote` (for a checkout of the tenant's own), `POST /v1/customers/{id}/coupons`, and `promotion_code` on `POST /v1/payment-links`.

**Phase 42:** the three ways to sign in. `GET /auth/methods` (which are on), `POST /auth/otp/request` (5 a minute per client address, always `202`) and `POST /auth/otp/verify` (10 a minute), `GET /auth/google/start` and `/auth/google/callback` (20 a minute; 404 unless `PAYRUDDER_GOOGLE_CLIENT_ID` and `_SECRET` are set; the callback verifies the ID token and hands the session to the console in a URL fragment). The sign-in, code and `/auth/me` answers carry `operator: true|false`, the server's verdict: the user's address is on `PAYRUDDER_OPERATOR_EMAILS` and verified. `operatorAllowed` lets an operator's **session** through the operator routes (and act as any tenant) in addition to the machine token `PAYRUDDER_OPERATOR_TOKEN`; a valid session of anybody else is `403 identity.operator.forbidden`, an invalid one `401`. Details: phase 42.
Billing pages: `GET /v1/payments` (tenant route; the ledger folded to payments, newest first, from payments-worker's internal ledger of the principal's own tenant) joins the invoice, plan, coupon, tax, dunning and report reads that the product app's pages call as a session acting on its own tenant (viewers GET only); the operator's ledger, reconciliation and invoice saga stay under `/admin/v1`.

**Phase 41:** `GET /app/v1/apps/{id}/dashboard` (and the operator's `GET /admin/v1/tenants/{tenant_id}/dashboard`) composes a period with the one before it, one block per currency, from billing's accounting summary and `GetDashboardStats`, plans' subscription report (by plan) and customer stats, the payment ledger and limits; sections fail independently (`unavailable`), a complete answer is cached for 15 seconds. See phase 41.

## As built: the invoice inbox and the hosted pages (Phase 54)

- `GET /v1/expense-inbox?app_id=`, `POST /v1/expense-inbox/rotate`, `GET /v1/expense-inbox/items?status=`, `GET /v1/expense-inbox/items/{id}/file`, `POST /v1/expense-inbox/items/{id}/resolve` and `GET|POST /v1/expense-rules`, `PUT|DELETE /v1/expense-rules/{id}` (tenant routes; billing-svc keeps the data, see its Phase 54 section).
- `POST /inbound/invoices` is the webhook of the mail provider that receives `invoices+<token>@<domain>` (Resend, Nafru or any provider that posts JSON). It is public and protected by a shared secret: `PAYRUDDER_INBOUND_SECRET` (environment only) sent as `Authorization: Bearer`, `X-Inbound-Secret` or `?secret=`; **without the secret the endpoint answers 404**. `PAYRUDDER_INBOUND_DOMAIN` is the domain the console shows in the address (empty: the token only). The body is the mail, or `{"type": "email.received", "data": {...}}` around it: `from`, `to` (a string, a list of strings, or of `{email}`), `subject`, `attachments[]` with `filename`, `content_type` and base64 `content`. The tenant is the token in the recipient address. Up to 6 MiB of body, 3 MiB of attachment bytes; a wrong secret is 401; a mail to an unknown or missing address is 200 and stores nothing. Set up the provider: point its inbound address (a catch-all or plus-address route on your inbound domain) at `https://<gateway>/inbound/invoices?secret=...`.
- The hosted pages (`/c/{token}` portal, `/i/{token}` invoice and payment pages) use one stylesheet written with the console's design tokens (`oklch` values of `payrudder-brand/DESIGN.md`), light and dark by `prefers-color-scheme`, responsive from 320 px; the promo-code fields carry `autocomplete="off"` and the password-manager opt-outs so a browser does not offer saved passwords there.

### Integrations routes (Phase 53)

The integrations screen of the console talks to `GET`/`PUT`/`DELETE /app/v1/apps/{id}/integrations/e-invoice` (+ `POST .../test`) and `GET`/`PUT`/`DELETE /app/v1/apps/{id}/integrations/mail` (+ `POST .../test`), all session routes. As with the payment providers nothing in a request names a tenant or an environment: both are the app's live ones, found through the person's membership; only the owner changes anything (`identity.go_live.owner_only`), any member reads, and a credential goes in once and is never in an answer (`no-store`). The e-invoice routes are forwarded to payments-worker's e-invoice account endpoints with the person as actor; the integrator must be `nilvera` or `webhook`, never the mock (the sandbox keeps the mock), and `test` asks the integrator whether the stored credentials work (`POST /admin/tenants/{tenant}/e-invoice-accounts/{environment}/check`: an integrator that can look up a registered business, Nilvera, is asked about a tax id, which needs valid credentials; one with nothing to ask answers `checked: configuration`). The mail routes call plans-svc's `MailIntegrationService` for the live tenant. `ResolveMailSender`, which returns the key in plaintext for the notifier, has no route.

## As built: Payrudder as a SaaS (Phase 55)

- With `PAYRUDDER_SAAS=on` (`Deps.Saas` set): `GET` and `PUT /app/v1/account/plan`, `GET /app/v1/account/plan/invoices`, `POST /app/v1/account/plan/invoices/{id}/pay-link`, `POST /app/v1/account/plan/portal-link`, and for the operator `GET /admin/v1/saas`, `GET /admin/v1/organizations`, `PUT /admin/v1/organizations/{account_id}/plan`, `DELETE /admin/v1/organizations/{account_id}/overrides`. With it off the plan page answers `enabled: false` and the writes `409 plans.saas.disabled`. The events used come from limits-svc (`CheckEntitlement` of the organisation's customer row as the platform tenant); the invoices, the payment link of an open invoice and the portal link are billing-svc's, called as the platform tenant after the gateway checked the caller's role and that the invoice is the organisation's own.
- `POST /v1/events`: for a live tenant the gateway asks plans-svc for the ingest policy (cached 30 s), refuses with `429 gateway.saas.events_quota` while the hard events quota is exhausted (cached 5 s), and after ingest answers meters the accepted events into the platform tenant as one event (`saas-events:<first accepted event id>`). Every step fails open. The platform tenant is left out of `GET /admin/v1/tenants`.
