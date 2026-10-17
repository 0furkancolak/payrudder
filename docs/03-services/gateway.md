# gateway

## Responsibility

The single public entry point. It terminates HTTP/JSON, authenticates API keys, resolves the tenant,
enforces per-tenant rate limits and quotas, and translates requests into gRPC calls to core services.
It holds **no business logic** and **no database**.

## Public HTTP API (v1)

All routes require `Authorization: Bearer qk_live_<prefix>_<secret>`. JSON bodies, UTF-8.

| Method & path | Maps to | Rate class |
|---|---|---|
| `POST /v1/events` | `ingest.v1.IngestService/IngestEvents` | `ingest` |
| `GET /v1/customers/{external_id}/entitlements/{meter_code}` | `limits.v1.LimitsService/CheckEntitlement` | `read_hot` |
| `POST /v1/customers` · `GET /v1/customers/{external_id}` | `plans.v1.PlansService/CreateCustomer`, `GetCustomer` | `write` / `read` |
| `POST /v1/meters` · `GET /v1/meters` | `CreateMeter`, `ListMeters` | `write` / `read` |
| `POST /v1/plans` · `POST /v1/plans/{id}/versions` | `CreatePlan`, `PublishPlanVersion` | `write` |
| `POST /v1/subscriptions` · `DELETE /v1/subscriptions/{id}` | `CreateSubscription`, `CancelSubscription` | `write` |
| `GET /v1/customers/{external_id}/usage?period=2026-11` | `aggregator.v1.UsageQueryService/GetCustomerUsage` | `read` |
| `GET /v1/invoices` · `GET /v1/invoices/{id}` · `GET /v1/invoices/{id}/pdf` | `billing.v1.BillingService/*` | `read` |
| `GET /v1/analytics/usage?…` | `analytics.v1.AnalyticsService/QueryUsage` | `analytics` |
| `POST /admin/v1/tenants` · `POST /admin/v1/tenants/{id}/api-keys` | `plans.v1.AdminService/*` | operator token only |

`POST /v1/events` request sketch:

```json
{
  "events": [
    {
      "idempotency_key": "req_8f2c…",
      "customer_external_id": "cus_42",
      "meter_code": "ai_tokens",
      "quantity": 1500,
      "event_time": "2026-11-03T10:15:00Z",
      "properties": { "model": "gpt-x", "region": "eu" }
    }
  ]
}
```

The response is always `202` for a structurally valid batch, with a per-event result
(`accepted` | `duplicate` | `rejected` + `error_code`). Whole-batch errors: `400` (malformed),
`401`, `413` (> 500 events or > 1 MiB), `429` (with `Retry-After`), `503`.

## Middleware chain

`recover → otel (server span, extract traceparent) → request-id → body limit → auth → tenant ctx → rate limit → route → grpc client (inject metadata)`

## Authentication & tenant resolution

1. Parse key `qk_{env}_{prefix}_{secret}`. `prefix` is 8 chars and indexed. `secret` is 32 random bytes, base62.
2. Look up `gw:apikey:{prefix}` in Redis → `{tenant_id, key_hash, status, tier}`.
   On a miss, call `plans.v1.AdminService/ResolveApiKey(prefix)` and cache for 60 s.
3. Constant-time compare `sha256(secret)` with `key_hash`. SHA-256 is enough because secrets are
   high-entropy random values, not passwords (see [06-multi-tenancy.md](../06-multi-tenancy.md#api-keys)).
4. Revocation: plans-svc deletes the cache key on revoke, so revocation takes effect immediately.
   The 60 s TTL only bounds how stale the cache can get if the delete fails.
5. Set gRPC metadata `x-quotify-tenant-id`, `x-quotify-api-key-id`, `x-request-id`. Downstream services
   trust this metadata only from the gateway (network policy in K8s. mTLS is out of scope).

## Rate limiting

- Algorithm: **GCRA** (generic cell rate algorithm) in one Redis Lua script, key
  `gw:rl:{tenant_id}:{rate_class}`. It's O(1), stores one timestamp per key, and never sleeps.
- Limits come from the tenant **tier** (`free`, `standard`, `enterprise`) in the API key cache entry:

  | Tier | `ingest` events/s (burst) | `read_hot` req/s | `write` req/s |
  |---|---|---|---|
  | free | 100 (500) | 50 | 5 |
  | standard | 2,000 (10,000) | 500 | 50 |
  | enterprise | 10,000 (50,000) | 2,000 | 200 |

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
| ingest-svc down / slow | gRPC deadline 2 s → `503` + `Retry-After: 1`. Idempotency keys make client retries safe |
| Redis down | API-key cache misses go to plans-svc. Rate limiting falls back to a local bucket |
| Client sends same batch twice | Each event reports `duplicate` the second time. No double count |

## Scaling notes

CPU-bound on JSON decoding and TLS. Scale horizontally on CPU (HPA at 70%).
Keep gRPC client connections pooled. Use one `grpc.ClientConn` per upstream with round-robin load balancing over headless Services.
