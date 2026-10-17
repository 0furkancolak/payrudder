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

  rpc CreateMeter(CreateMeterRequest) returns (Meter);
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
  rpc CreateTenant(CreateTenantRequest) returns (Tenant);
  rpc UpdateTenantSettings(UpdateTenantSettingsRequest) returns (Tenant); // grace period, tier
  rpc CreateApiKey(CreateApiKeyRequest) returns (CreateApiKeyResponse);   // returns plaintext once
  rpc RevokeApiKey(RevokeApiKeyRequest) returns (ApiKey);
  rpc ResolveApiKey(ResolveApiKeyRequest) returns (ResolvedApiKey);       // gateway only
}

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
  int64 limit_quantity = 2;            // per period
  repeated int32 alert_thresholds_pct = 3; // e.g. [80, 100]
}
```

## Owned data (`plans_db`)

`tenants`, `tenant_settings`, `api_keys`, `customers`, `meters`, `plans`, `plan_versions`,
`plan_prices`, `plan_entitlements`, `subscriptions`, `outbox`. DDL is in [05-data-model.md](../05-data-model.md#plans_db).

Rules:
- `meters` and `plan_versions` are **append-only**. Changing a price means publishing a new version.
  This is what makes historic invoices reproducible and re-rating explicit.
- A customer has **at most one active subscription** at a time (exclusion constraint on `tstzrange`).

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
