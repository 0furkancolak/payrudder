# plans-svc

## Responsibility

System of record for **tenants, API keys, customers, meters, plans, plan versions, prices,
entitlements and subscriptions**. It's the control plane: low write volume, read-heavy, strongly consistent.

## gRPC API (sketch)

```proto
syntax = "proto3";
package quotify.plans.v1;

service PlansService {
  rpc CreateCustomer(CreateCustomerRequest) returns (Customer);
  rpc GetCustomer(GetCustomerRequest) returns (Customer);          // by external_id
  rpc ResolveCustomer(ResolveCustomerRequest) returns (Customer);  // internal, hot path, cached by callers
  rpc ListCustomers(ListCustomersRequest) returns (ListCustomersResponse);

  rpc SetPaymentMethod(SetPaymentMethodRequest) returns (Customer); // sandbox: only pm_sandbox_* tokens accepted
  rpc CreateMeter(CreateMeterRequest) returns (Meter);              // quantity_scale (0..6) + unit_label, fixed per code
  rpc GetMeter(GetMeterRequest) returns (Meter);                   // by code, latest version
  rpc ListMeters(ListMetersRequest) returns (ListMetersResponse);

  rpc CreatePlan(CreatePlanRequest) returns (Plan);
  rpc PublishPlanVersion(PublishPlanVersionRequest) returns (PlanVersion); // immutable after publish
  rpc GetPlanVersion(GetPlanVersionRequest) returns (PlanVersion);

  rpc CreateSubscription(CreateSubscriptionRequest) returns (Subscription);
  rpc CancelSubscription(CancelSubscriptionRequest) returns (Subscription);
  rpc ListSubscriptionsForPeriod(ListSubscriptionsForPeriodRequest)
      returns (stream Subscription);                                // used by billing-svc
}

service AdminService {                                               // operator token only
  rpc CreateAccount(CreateAccountRequest) returns (Account);       // creates the sandbox + live tenant pair
  rpc UpdateTenantSettings(UpdateTenantSettingsRequest) returns (Tenant); // grace period, tier (currency, timezone immutable)
  rpc CreateApiKey(CreateApiKeyRequest) returns (CreateApiKeyResponse);   // returns plaintext once
  rpc RevokeApiKey(RevokeApiKeyRequest) returns (ApiKey);
  rpc ResolveApiKey(ResolveApiKeyRequest) returns (ResolvedApiKey);       // gateway only
}

message CreateAccountRequest {
  string name = 1;
  string currency = 2;        // required, ISO 4217 from the supported list (TRY, USD, EUR, GBP, JPY, KWD…); no default
  string timezone = 3;        // required, IANA name (e.g. "Europe/Istanbul"); defines period boundaries; no default
  google.protobuf.Duration grace_period = 4;  // required, 0..7 days
  string tier = 5;
}
// Both tenants of an account get the same currency/timezone/grace. CreateAccount rejects a missing field with
// INVALID_ARGUMENT: the operator (or the onboarding form in the admin UI) must choose explicitly.

message Price {
  string meter_code = 1;
  oneof model {
    PerUnit per_unit = 2;              // unit_price as decimal string in minor units, e.g. "0.002"
    TieredGraduated tiered = 3;        // tiers: up_to (nullable = ∞), unit_price, flat_fee_minor
    Package package = 4;               // package_size, package_price_minor
  }
}
message Entitlement {
  string meter_code = 1;
  string limit = 2;                    // per period, decimal string in meter units (stored as scaled units)
  repeated int32 alert_thresholds_pct = 3; // e.g. [80, 100]
}
```

## Owned data (`plans_db`)

`accounts`, `tenants`, `api_keys`, `customers`, `meters`, `plans`, `plan_versions`,
`plan_prices`, `plan_entitlements`, `subscriptions`, `outbox`. DDL is in [05-data-model.md](../05-data-model.md#plans_db).

Rules:
- `meters` and `plan_versions` are **append-only**. Changing a price means publishing a new version.
  This is what makes historic invoices reproducible and re-rating explicit.
- A customer has **at most one active subscription** at a time (exclusion constraint on `tstzrange`).
- Tenant `currency` and `timezone` are **immutable** (DB trigger). Changing them would re-cut periods and re-price history.
  `grace_period` may change and applies from the next period (snapshotted by aggregator).
- A meter's `quantity_scale` is fixed for its code: a new meter version with a different scale is rejected.

## Produced messages

| Topic | When | Key |
|---|---|---|
| `plans.entitlements.v1` (compacted) | Subscription created/cancelled, or plan version with entitlements published | `{tenant_id}/{customer_id}/{meter_code}` |

Produced through plans-svc's own **outbox** in the same transaction as the subscription change.
Tombstone (null value) when a subscription ends.

## Consumed messages

None.

## Failure modes

| Failure | Behaviour |
|---|---|
| DB unavailable | All writes fail with `UNAVAILABLE`. Gateway and ingest use cached reads |
| Outbox relay lag | limits-svc sees entitlement changes late. Bounded by relay lag alert (> 30 s) |
| Concurrent `CreateSubscription` for same customer | Exclusion constraint → one wins, the other gets `ALREADY_EXISTS` |

## Scaling notes

Low QPS except `ResolveCustomer` / `GetMeter`, which callers cache (LRU 10k entries, 5 min TTL,
and invalidation is unnecessary because meters are immutable and customers are only ever added).
Two replicas for availability.

## As built: entitlement outbox (Phase 04)

`CreateSubscription` and `CancelSubscription` insert the `plans.entitlements.v1` rows in their own transaction
(`outbox.ingested_at` replaced `created_at`, migration 00002) and `NOTIFY outbox_new`. The relay (`pkg/outbox`, run with the
system pool) starts when `QUOTIFY_KAFKA_BROKERS` is set.
