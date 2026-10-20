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
  rpc ListSubscriptionsFeeDue(ListSubscriptionsFeeDueRequest)
  rpc AdvanceSubscriptionFee(AdvanceSubscriptionFeeRequest)
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

## As built: subscription lifecycle (Phase 13)

`CreateSubscription` takes `trial_days` (0..365) and `currency` (default: the customer's billing currency, then the tenant's). The
currency must have a fixed fee on the plan version, and a plan version with metered prices needs a usage price set in it. New RPCs:
`GetSubscription`, `PauseSubscription`, `ResumeSubscription`, `ScheduleCancellation` (`cancel=false` withdraws it) and `ChangePlan`. State
moves through `pkg/subscription` (`Next`). `ChangePlan` ends the subscription at the change instant and starts a new one on the other
plan version with `changed_from_id` set and the same billing anchor; it returns the old and new plan's share of the month's fixed fee.

A subscription is invoiced on the calendar months of the tenant: `billing_anchor` is the first instant of the month it started in.
`PublishPlanVersion` accepts `fees` (a fixed fee per currency **and interval**) and `currency_prices` (usage prices in the
same currencies, covering exactly the meters of the base prices).

## As built: yearly plans and fees in advance (Phase 19)

A plan version's fixed fee is per interval and currency: the monthly fee in the tenant's currency is `base_fee_minor`, every other
combination is a `plan_version_fees` row (`interval` is part of its key, so a yearly fee in the tenant's currency is listed in `fees`).
A subscription is created `month` or `year`, needs a fee for that interval in its currency, and has a `fee_timing`:

- `advance` (the default): the fixed fee is invoiced at the start of each billing period. The billing anchor is the subscription's start
  (the trial end for a trial), so periods are anniversaries (`pkg/subscription.PeriodEnd` clamps a 29 February or a 31st). `fee_due_at` is
  the start of the next period to invoice.
- `arrears`: the behaviour of phases 13–18 (the fee rides on the calendar month's usage invoice, calendar-month anchor). Monthly only.
- A subscription whose provider renews it itself (Paddle) is always `arrears` here and has no `fee_due_at`: the provider bills its fee.

`ListSubscriptionsFeeDue(now, after, page_size)` is billing-svc's work list: advance, Quotify-renewed, not cancelled, `fee_due_at <= now`
and still running at that period's start, each with the period `[start, end)` and the fee. `AdvanceSubscriptionFee(subscription_id,
period_start)` moves `fee_due_at` to the period's end by compare-and-set, so calling it twice, or with a time that is not the next period,
changes nothing (the latter is refused). `ChangePlan` of an advance-billed subscription is refused: those are changed with `ChangeSubscription` (phase 20). The subscription report
counts a yearly fee as a twelfth in MRR.

## As built: plan and interval changes (Phase 20)

`ChangeSubscription` and `PreviewSubscriptionChange` (same request) move an advance-billed, Quotify-renewed subscription to another plan version and/or interval.
`pkg/subscription.QuoteChange` is the one calculation behind both: the preview returns it, the change performs it. Defaults by direction: an upgrade
(or an equal-priced switch) and monthly-to-yearly are **immediate and prorated**; a cheaper plan and yearly-to-monthly **wait for the end of the paid period** and move no money.
`timing` (`IMMEDIATELY`, `PERIOD_END`) and `proration` (`PRORATE`: credit the unused old time and, within an interval, charge the new fee for the rest of the period, keeping the cycle;
`FULL`: credit the unused old time and start a new cycle now at the full price; `NONE`: no money now, new fee from the next period) override the defaults.
An immediate change of interval always starts a new cycle (the year starts now) so it needs `PRORATE` or `FULL`.

An immediate change ends the old subscription and starts its successor in one transaction (`changed_from_id`, the quote's anchor and next fee period, `fee_due_at`). The money it moved
(credit = the unused, prepaid part of the old fee; charge = the new fee) is written to `subscription_adjustments` for billing-svc. A change that waits is a pending
`subscription_changes` row (one per subscription; a new change replaces it; `CancelScheduledChange` withdraws it) that `ApplyScheduledChanges(now)` applies when its time comes: it ends
the old subscription at the period end and starts the new one with `fee_due_at` at that instant, so the new plan's first fee is invoiced by the next fee run. A change is cancelled
instead of applied if the subscription ended, is paused, past due, or ending by then. A change during a trial moves no money (the trial and the first fee date stay). A change is refused with
`ABORTED` while the fee of the current period has not been invoiced yet (there would be nothing to credit), with `FAILED_PRECONDITION` for arrears, provider-owned, paused, past-due or ending subscriptions, and
`INVALID_ARGUMENT` for combinations that make no sense. `ListSubscriptionAdjustments` and `MarkSubscriptionAdjustmentBilled` are billing-svc's work list. `Subscription.scheduled_change` shows a waiting change.

## As built: following a provider's plan changes (Phase 21)

`SyncProviderSubscription` also takes `plan_version_id` and `interval`. For a provider-owned subscription whose provider now bills another plan version or interval, the subscription switches to a successor (`changed_from_id`, same anchor, `fee_timing = arrears`, owner and provider id carried over; the ended one gives the id up, since it is unique) at the provider's event time, with no money and no adjustment: the provider billed the change. Events keep naming the subscription Quotify first linked, so the call resolves them along the chain to the one that holds the provider id; a subscription outside that chain that names a taken provider id is still refused.

## As built: coupons and tax rates (Phase 22)

`CreateCoupon`, `ListCoupons`, `DeactivateCoupon`; `ApplyCoupon`/`RemoveCoupon` (or `coupon_code` on `CreateSubscription`). A coupon is a percentage (`percent_bps`) or an amount in a currency, `once`, `repeating` (N fees) or `forever`, with an optional redemption limit and expiry; redeeming counts in one statement so two redemptions cannot take the last one. A subscription has at most one; it moves to the successor in a plan change. Only subscriptions Quotify invoices in advance take one. `ListSubscriptionsFeeDue` returns the discount of the due fee (`pkg/subscription.CouponDiscount`), and `AdvanceSubscriptionFee` counts the period when it advances it.
`GetTaxRates`/`SetTaxRates` hold the tenant's rates by customer country (`tax_rates`; empty country = every other country), replaced as a whole. Billing reads them when it raises an invoice.


## As built: webhooks and early-cancel credit (Phase 23)

`WebhookService` (same server, `webhooks.proto`) owns the tenant's webhook endpoints, the events and their deliveries (tables `webhook_endpoints`, `webhook_events`, `webhook_deliveries`, RLS like the rest). Subscription RPCs write their event in the same transaction as the change (`Server.emit`);
`RecordTenantEvent` takes the events billing-svc hands over. The dispatcher (`internal/webhooks`, run as a goroutine when `QUOTIFY_SECRETS_KEY` is set) claims due deliveries with `FOR UPDATE SKIP LOCKED` and a two-minute lease, signs and posts through `pkg/safehttp`, and retries on the backoff in [16-webhooks.md](../16-webhooks.md).
`CancelSubscription` with `credit_unused` and the provider cutover record the unused prepaid fee as a `subscription_adjustments` row (`Server.creditUnusedFee`).
