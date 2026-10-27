# plans-svc

## Responsibility

System of record for **tenants, API keys, customers, meters, plans, plan versions, prices,
entitlements and subscriptions**. It's the control plane: low write volume, read-heavy, strongly consistent.

## gRPC API (sketch)

```proto
syntax = "proto3";
package payrudder.plans.v1;

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
  string currency = 2;        // required, any active ISO 4217 currency (phase 40: 153 codes with their exponents); no default
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
- `meters` and `plan_versions` are **append-only**. Changing a price means publishing a new version. A version needs a price, a base fee or a quota: a free tier is a version with a fee of nothing and quotas only.
  This is what makes historic invoices reproducible and re-rating explicit.
- A customer has **at most one active subscription** at a time (exclusion constraint on `tstzrange`).
- Tenant `currency` and `timezone` are **immutable** (DB trigger). Changing them would re-cut periods and re-price history.
  `grace_period` may change and applies from the next period (snapshotted by aggregator).
- A meter's `quantity_scale` is fixed for its code: a new meter version with a different scale is rejected.

## Produced messages

| Topic | When | Key |
|---|---|---|
| `plans.entitlements.v1` (compacted) | Subscription created/cancelled, or plan version with entitlements published | `{tenant_id}/{customer_id}/{meter_code}` |
| `subscription.trial-will-end.v1` | The trial-ending reminder records a notice (below) | `{tenant_id}/{customer_id}` |

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
system pool) starts when `PAYRUDDER_KAFKA_BROKERS` is set.

## As built: subscription lifecycle (Phase 13)

`CreateSubscription` takes `trial_days` (0..365; unset takes the plan version's own default, `PlanVersion.trial_days`, set when the version is published; `0` is no trial whatever the default is) and `currency` (default: the customer's billing currency, then the tenant's). The
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

`ListSubscriptionsFeeDue(now, after, page_size)` is billing-svc's work list: advance, Payrudder-renewed, not cancelled, `fee_due_at <= now`
and still running at that period's start, each with the period `start, end)` and the fee. `AdvanceSubscriptionFee(subscription_id,
period_start)` moves `fee_due_at` to the period's end by compare-and-set, so calling it twice, or with a time that is not the next period,
changes nothing (the latter is refused). `ChangePlan` of an advance-billed subscription is refused: those are changed with `ChangeSubscription` (phase 20). The subscription report
counts a yearly fee as a twelfth in MRR.

## As built: plan and interval changes (Phase 20)

`ChangeSubscription` and `PreviewSubscriptionChange` (same request) move an advance-billed, Payrudder-renewed subscription to another plan version and/or interval.
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

## As built: seats, a quantity on the subscription

A subscription has a `quantity` (seats, licences; migration `00060`, `subscriptions.quantity`, 1 to 100000, default 1). It is a **plain multiplier of the plan's fixed fee**: `plansqry.SubscriptionFee` (the fee run, the early-cancel credit) and the quote of a change price `FeeIn(plan version, interval, currency) × quantity` (`plansqry.TimesQuantity`). Usage prices, quotas and entitlements are not multiplied. `CreateSubscription.quantity` (0 is 1) and `ChangeSubscriptionRequest.quantity` (0 keeps it) set it; both refuse a negative quantity or one above 100000 with `INVALID_ARGUMENT`, and a quantity above 1 on a subscription that is not Payrudder-run and billed in advance with `plans.subscription.quantity_needs_advance` (the arrears fee and re-rating are not quantity-aware, and a provider prices its own quantities).

A change of quantity is a change like any other: `Price` quotes the old sum (plan fee × old quantity) and the new one (× new quantity) through the same `subscription.QuoteChange`, so the preview is what is made. When the plan version and interval stay, the default is **immediate and prorated in both directions** (more seats: the unused time of the old seats is credited and the new seats are charged for the rest of the period, so the net is the added seats for the time left; fewer seats: the net is a credit that billing turns into a credit balance), `timing = PERIOD_END` schedules it instead. A change of plan keeps the direction rule (a cheaper total waits for the period end). The credit and the charge are after the coupon in force, as for a plan change (`couponsqry.InForce`), and the successor keeps the coupon. A waiting change stores the quantity it was priced at (`subscription_changes.quantity`; NULL for one made before, which keeps the subscription's own) and `ApplyScheduled` applies it. Every other successor (`SwitchTo`: provider sync, cutover, plan change) keeps the quantity of the subscription it replaces. `ChangeQuote.quantity`, `ScheduledChange.quantity` and `Subscription.quantity` carry it in the API and the webhook events. The console does not show or set it yet.

## As built: following a provider's plan changes (Phase 21)

`SyncProviderSubscription` also takes `plan_version_id` and `interval`. For a provider-owned subscription whose provider now bills another plan version or interval, the subscription switches to a successor (`changed_from_id`, same anchor, `fee_timing = arrears`, owner and provider id carried over; the ended one gives the id up, since it is unique) at the provider's event time, with no money and no adjustment: the provider billed the change. Events keep naming the subscription Payrudder first linked, so the call resolves them along the chain to the one that holds the provider id; a subscription outside that chain that names a taken provider id is still refused.

## As built: coupons and tax rates (Phase 22)

`CreateCoupon`, `ListCoupons`, `DeactivateCoupon`; `ApplyCoupon`/`RemoveCoupon` (or `coupon_code` on `CreateSubscription`). A coupon is a percentage (`percent_bps`) or an amount in a currency, `once`, `repeating` (N fees) or `forever`, with an optional redemption limit and expiry; redeeming counts in one statement so two redemptions cannot take the last one. A subscription has at most one; it moves to the successor in a plan change. Only subscriptions Payrudder invoices in advance take one. `ListSubscriptionsFeeDue` returns the discount of the due fee (`pkg/subscription.CouponDiscount`), and `AdvanceSubscriptionFee` counts the period when it advances it.
A coupon is a discount on a fee Payrudder invoices in advance, so a move to a successor that cannot hold one (a provider-run one, or one billed in arrears) would lose it. A provider migration therefore refuses a subscription that has one with `plans.migration.coupon_blocks_move`: `StartProviderMigration` for a provider-run target (the customer subscribes there later, too late to refuse), and the cutover itself for any successor `subscriptionscmd.CarriesCoupon` says cannot carry it. Remove the coupon first (`RemoveCoupon`), knowing the discount ends. A Payrudder-run, in-advance target carries the coupon with its periods used.
`GetTaxRates`/`SetTaxRates` hold the tenant's rates by customer country (`tax_rates`; empty country = every other country), replaced as a whole. Billing reads them when it raises an invoice.


## As built: promotion codes and the redemption ledger (Phase 46)

A **coupon** is the definition of a discount (`percent_bps`, `amount_minor` + currency, or `bonus_percent_bps` extra credits on a credit purchase; `applies_to` says where it works: subscription fee, credit purchase, payment link or invoice). A **promotion code** is what a customer types: many codes point to one coupon, each with its own `max_redemptions`, `expire_time`, `first_purchase_only`, `one_per_customer`, a minimum amount, `allowed_plans` / `allowed_packs` / `allowed_currencies` and `restore_on_reversal`. Codes are compared in any letter case (`upper(code)` is unique per tenant). A new coupon also gets a code with its own text unless `without_default_code`; the migration (`00030_promotions.sql`) gave every existing coupon one, and is reversible.

`coupon_redemptions` is the ledger: one row per use, with the coupon, the code, the customer, the target (`subscription`, `credit_purchase`, `payment`; the id is the subscription or the invoice), the discount or bonus credits, the currency, `reversed_at`. `coupons.redeemed` and `promotion_codes.redeemed` are the count of it. Claiming is two conditional `UPDATE`s (`ClaimPromotionCode`, `ClaimCoupon`: `... WHERE redeemed < max_redemptions`) inside the checkout's transaction, so 50 checkouts racing for the last 7 places give exactly 7 (tested); the code's row lock also serialises the one-per-customer check. A unique index on `(target_kind, target_id)` of live rows makes asking twice for one purchase one redemption. A void or full refund calls `ReversePromotion`: the row is marked reversed, and the place goes back to the count only for a code with `restore_on_reversal` (default no, as before). `RemoveCoupon` reverses a subscription's redemption the same way, so the subscription can take another coupon.

`RedeemPromotion` is what a checkout calls (billing-svc, or a tenant's own page through `POST /v1/promotions/quote` with `preview`): it checks the rules (`coupons/model`, pure and table-tested), quotes the discount or bonus and, unless `preview`, claims it. A discount never takes the total below zero; an amount coupon applies only in its currency (`coupon.currency_mismatch`); a percentage rounds half to even. One coupon per subscription or purchase. Refusals carry `coupon.*` keys: `not_found`, `inactive`, `expired`, `exhausted`, `not_applicable`, `currency_mismatch`, `already_used`, `first_purchase_only`, `below_minimum`, `customer_required`, `already_applied`, `code_taken`.

**Affiliate hook (phase 47).** `GrantCustomerCoupon` (`customer_coupons`) puts a coupon in wait for a customer, once per `(customer, source, source_ref)`; the customer's next eligible checkout that types no code uses it. A referral programme calls it with `source = "affiliate"`; nothing in this schema needs to change for that. `coupons.id` is the stable identity a programme stores.

Bulk generation (`CreatePromotionCodes`): the codes named, or `count` (1 to 1000) random ones, `prefix` + 8 characters from an alphabet without `0 O 1 I L`, all with the template's rules.

## As built: the affiliate program (Phase 47)

Module `affiliates` (ADR 0020 shape: `model/` pure rules, `command/`, `query/`, `portal/`, one `grpc.go`). Tables (all tenant-scoped, forced RLS): `affiliate_programs`, `affiliates`, `affiliate_clicks_daily`, `affiliate_attributions` (set once; a trigger refuses UPDATE/DELETE), `affiliate_attribution_rejections`, `affiliate_commissions`, `affiliate_reversals`, `affiliate_payouts`, `affiliate_journal` (append-only, balanced per payout and currency by a deferred trigger). Services: `AffiliateService` (owner, key-scoped), `AffiliatePublicService` (`/r/<code>` clicks, system pool), `AffiliatePortalService` (a signed-in partner). billing-svc's `invoice.paid`, `invoice.voided` and `credit_note.created` events are read in `RecordTenantEvent` (an `EventHook`), a job approves commissions after their hold (`PAYRUDDER_AFFILIATE_APPROVE_EVERY`, default 1h). See [phase 47.

## As built: webhooks and early-cancel credit (Phase 23)

`WebhookService` (same server, `webhooks.proto`) owns the tenant's webhook endpoints, the events and their deliveries (tables `webhook_endpoints`, `webhook_events`, `webhook_deliveries`, RLS like the rest). Subscription RPCs write their event in the same transaction as the change (`Server.emit`);
`RecordTenantEvent` takes the events billing-svc hands over. The dispatcher (`internal/webhooks/worker`, run as a goroutine when `PAYRUDDER_SECRETS_KEY` is set) claims due deliveries with `FOR UPDATE SKIP LOCKED` and a two-minute lease, signs and posts through `pkg/safehttp`, and retries on the backoff in [16-webhooks.md](../16-webhooks.md).
`CancelSubscription` with `credit_unused` and the provider cutover record the unused prepaid fee as a `subscription_adjustments` row (`Server.creditUnusedFee`).


## As built: live is earned, keys with scopes (Phase 45)

**Tenant directory (phase 28, [ADR 0018](../adr/0018-tenant-isolation-modes.md)).** plans-svc owns where each tenant's data lives: `tenants.cell` (empty = the pooled database), `region`, `placement_status` (`ready` or `moving`), migration `00062`. `DirectoryService` is operator-only and internal (no gateway route): `GetPlacement`, `ListPlacements`, and the three steps of a move, `BeginMove` (ready to moving; refused while any cross-tenant job is unconverted, if the target is a cell, or when the tenant is already there), `FinishMove` (moving to ready in the new place, the check again) and `AbortMove`. The steps are transitions checked under a row lock in the module `internal/directory` (`command/`, `query/`, `grpc.go`); their refusals are plain statuses whose message starts with a reason word (`cells_not_ready`, `move_in_progress`, `same_place`, `not_moving`, `try_again`), not console error keys. Services read the directory through `pkg/tenantdir`, cached. Every tenant is pooled and `ready`; nothing reads the placement of a tenant in plans-svc's own data path.

A live tenant is created `inactive` (`tenants.status`, migration `00045`); a sandbox tenant is always `active`. `IssueAPIKey` and webhook endpoint creation refuse an inactive or suspended live tenant (`plans.api_key.tenant_not_active`, `plans.webhook.tenant_not_active`), and triggers in the database refuse the same writes and any key whose `env` disagrees with its tenant. The module `internal/golive` (`command/`, `query/`, `grpc.go`) computes the checklist (`GetMyGoLive`: owner e-mail verified, legal profile, payment provider, plan published, terms accepted) and activates (`ActivateMyApp`, owner only, on a locked tenant row, the checklist recomputed inside the transaction); the provider item is passed in by the gateway, which asks payments-worker. `AdminService.SetTenantStatus` lets an operator suspend, reinstate, or activate with a reason. Keys: `CreateMyApiKey` takes a role and permissions (phase 53, below), `RotateMyApiKey` issues a replacement and sets `expires_at` on the old key, `ResolveApiKey` returns the tenant status, the scope and `expire_time`, `TouchApiKey` records use. See [ADR 0023](../adr/0023-live-and-sandbox-guards.md).

**Phase 45 D:** `internal/promote` (`query/diff.go` the pure rules, `command/` the composition over the meters, plans, tax, coupons, mail settings and webhook modules, `grpc.go`, `wire.go`) serves `IdentityService.PromoteMyApp`; migration `00046` adds `config_promotions` (the audit of applied runs, one direction enforced by a trigger). See [ADR 0023](../adr/0023-live-and-sandbox-guards.md).

**Phase 45 E:** `internal/sandbox` (`steps.go` the steps and their transports, `local.go` plans-svc's own part, `runner.go` the job runner and the retention sweep, `command/`, `query/`, `api/`) serves `ResetMySandbox` and `GetMySandboxReset`; migration `00049` adds `sandbox_resets` and the narrow DELETE grants. Settings: `PAYRUDDER_SANDBOX_RETENTION` (2160h), `PAYRUDDER_SANDBOX_RESET_COOLDOWN` (5m), and the addresses of the services a reset empties (`PAYRUDDER_INGEST_ADDR`, `_AGGREGATOR_ADDR`, `_ANALYTICS_ADDR`, `_BILLING_ADDR`, `PAYRUDDER_PAYMENTS_URL` with `PAYRUDDER_PAYMENTS_INTERNAL_TOKEN`; an empty one skips that step).

## As built: people, accounts and sessions (Phase 38)

`IdentityService` (`identity.proto`, module `internal/identity` with `credential/`, `command/`, `query/` and one `grpc.go`) is the people side: `SignUp` (user, account and owner membership in one transaction), `SignIn`, `SignOut`, `GetSession`, and the account-scoped `GetMyAccount`, `CreateMyApp`, `ListMyApps`, `GetMyApp`, `ResolveMyTenant`, `CreateMyApiKey`, `ListMyApiKeys`, `RevokeMyApiKey`. Tables `users`, `account_members`, `user_sessions` and `user_onboarding` (no `tenant_id`, no `plans_app` grants) are on the system pool only. Every account-scoped call carries the user id the gateway resolved from the session and finds rows only through `account_members` in SQL; another account's objects are "not found". Apps and keys are made by the same code as the operator path (`admin.Server.InsertApp`, `IssueAPIKey`, `RevokeKey`). Passwords: argon2id 19 MiB / t=2 / p=1, at most four hashes at once per replica; sessions: `qs_` tokens, SHA-256 stored, 14 days sliding, 90 days at most. Details, limits and the threat model: phase 38.

**Onboarding record** (`GetMyOnboarding`, `UpdateMyOnboarding`; module `internal/identity/onboarding`, migration `00059_user_onboarding.sql`). What the console remembers for a person, not for an account: the first-run wizard's progress and the page tours already seen, so a second browser does not start them again. One row per user in `user_onboarding` (`wizard` jsonb, `seen_tours text[]`), on the system pool like the other person tables; the user id is the one the gateway resolved from the session. An update is a **merge**, never a replacement, applied under a row lock (`SELECT … FOR UPDATE` after an `INSERT … ON CONFLICT DO NOTHING`) so two browsers take turns: the wizard fields present in the request overwrite the stored ones and the others stay, a flag the wizard has set (`customer`, `event`, `hidden`) stays set whatever a stale browser says, `seen_tours` are added to the set (a union, so no browser's tour is lost), and `forget_tours` removes tours afterwards (a person asking to see one again). The last writer wins a wizard field both changed. Validated: status and step are the wizard's own names, currency three capital letters, time zone and ids short and printable, a tour id 1 to 64 of letters, digits, dot, dash, underscore, at most 200 tours; a bad value is `plans.request.invalid` with the field.

**Phase 39:** `SignUp` mails a verification link and opens no session (the same answer for a taken address, whose owner is warned instead); `VerifyEmail`, `ResendVerification`, `RequestPasswordReset` and `ResetPassword` use single-use, hashed, expiring tokens (`user_tokens`, migration `00019`); mail to people is sent by plans-svc itself (`identity/usermail`, `pkg/mailx`) through `SMTP_ADDR` / `MAIL_FROM` / `PAYRUDDER_APP_URL`, in six languages. A live API key needs a verified address. Members: `InviteMember`, `ListInvitations`, `ResendInvitation`, `RevokeInvitation`, `LookupInvitation`, `AcceptInvitation` (new user) and `AcceptInvitationAsUser`, `UpdateMemberRole`, `RemoveMember`, `RenameMyAccount` (table `account_invites`, migration `00020`); role rules in `identity/command/members.go`; changes that could remove an owner serialise on `pg_advisory_xact_lock(account)` so an account always keeps one. See phase 39.

**Phase 42:** `RequestLoginCode` / `VerifyLoginCode` (a six-digit code, HMAC-SHA256 with `PAYRUDDER_OTP_PEPPER` and a per-code salt at rest, 10 minutes, single use, five tries, one a minute and five an hour per address; an address without an account gets one when the code is verified) and `SignInWithIdentity` (Google: found by `(provider, subject)` in `user_identities`, else linked to the user with the same verified address, else created; an address the provider did not verify is refused). Both claim a mailbox: an account whose address was never verified loses its password and its other sessions. Tables `login_codes` and `user_identities` (migration `00021`). Phase 41.

**Quotas:** an entitlement has a `window` (`QUOTA_WINDOW_MONTH`, the billing period and the default, or `HOUR`, `DAY`, `WEEK` in the
tenant's time zone) and a `mode` (`HARD`, the default: `allowed` turns false at the limit; `SOFT`: allowed stays true and the usage over the limit
is billed at the meter's price, so the plan version must price the meter; `NOTIFY`: alerts only). A price can carry `included`, a quantity of
every billing period that is free (stored as `included_units` in `plan_prices.config`; billing-svc subtracts it before the model, so a late
batch crossing it costs only what is above it). `SetEntitlementOverride` / `DeleteEntitlementOverride` / `ListEntitlementOverrides` give one
customer its own limit on a meter (`customer_entitlement_overrides`, migration 00052): `internal/entitlements/command` merges the overrides into
what it publishes for each subscription, and a change republishes every subscription of the customer that has not ended (a deleted override on a
meter no plan limits becomes a tombstone). Meters have a free-form `category` (at most 64 characters) and a free-form `unit_label` (at most 32
characters). Year and lifetime windows and the `UNIQUE`/`LAST` aggregations are not offered: the counters could not keep them truthfully (see
limits-svc).

## As built: roles and permissions on keys, the tenant's own e-mail provider (Phase 53)

**Keys.** Migration `00054` replaced `api_keys.scope` with `role` and `permissions text[]` (existing keys: `full` became `admin` with every permission, `ingest` became `ingest_only` with `events:write`, `read` became `read_only` with every `:read`). The vocabulary, the fixed roles and the validation live in `pkg/apikey` (`permission.go`: `Permissions`, `Roles`, `RolePermissions`, `Resolve`, `RequiredPermission`); `domain.KeyGrant` reads a request's role and permissions, or an older scope, and `Issue(ctx, tenant, role, permissions)` stores them. A fixed role takes its own set (permissions given with it must lie inside it); `custom` needs at least one known permission; neither given is `admin`. A rotation issues a replacement with the old key's role and permissions. `ResolveApiKey` and `ApiKey` carry `role` and `permissions` (the role `admin` always lists every permission that exists, so one added later reaches the admin keys already issued); the old `scope` field is still filled as a summary for older readers and enforces nothing. Errors: `identity.api_key.bad_role` (console path), `plans.api_key.bad_role` (operator path). The environment of a key is its tenant's (a trigger refuses any other) and a live key needs an active live tenant and a confirmed address, as before; the console makes sandbox keys only in its sandbox view and live keys only in its live view.

**The tenant's own e-mail provider.** `MailIntegrationService` (`mail_integration.proto`, module `internal/mailintegration` with `command/`, `query/`, `grpc.go`; migration `00055`, table `tenant_mail_integrations`, forced RLS, `plans_app` only). One provider so far, `nafru` (Nafru Email, Resend-compatible): a base URL, an API key and a From address. `PutMailIntegration` refuses a sandbox tenant (`plans.mail_integration.live_only`: its mail always goes to the sandbox sink), validates the URL like a webhook endpoint (https, no private address unless `PAYRUDDER_WEBHOOK_ALLOW_PRIVATE`), asks the provider to accept the key (`GET /domains`; `plans.mail_integration.refused` or `.unreachable`) and only then stores it, sealed with `secretbox` (AAD `mail-integration:<tenant>`; `plans.mail_integration.not_configured` without `PAYRUDDER_SECRETS_KEY`) with its last four characters as a hint. The key is never returned. `TestMailIntegration` checks the key and sends one test mail through it, records what it found (`checked_at`, `check_problem`) and answers `ok`/`problem` instead of an error. `ResolveMailSender` returns the plaintext connection for the notifier and is not routed by the gateway.

## As built: Payrudder as a SaaS (Phase 55)

- Module `internal/saas` (`model` the pure rules and the seeded plans, `query` the platform tenant, an organisation's standing and the views, `command` bootstrap, ensure, guard, plan change, overrides, sweep, and `grpc.go`): `SaasService` (`GetPlatform`, `GetMyPlan`, `ChangeMyPlan`, `ResolveIngestPolicy`, and for the operator `ListOrganizations`, `SetOrganizationPlan`, `ClearOrganizationOverrides`). Migration `00056_saas.sql` adds `accounts.platform` (a unique partial index allows one).
- `PAYRUDDER_SAAS` (`on` or `off`, default off), `PAYRUDDER_SAAS_ENVIRONMENT` (`live` or `sandbox`, chosen when the platform tenant is made), `PAYRUDDER_SAAS_CURRENCY`, `PAYRUDDER_SAAS_TRIAL_DAYS`, `PAYRUDDER_SAAS_SWEEP_EVERY`. At start, with the mode on, `Bootstrap` makes the platform account, app and tenant (under a *tried* advisory lock, so replicas starting together neither duplicate nor each hold a pooled connection while waiting) and seeds the meters and the plans whose code does not exist; an edited plan is never overwritten. A sweep every minute makes accounts without a subscription in force customers on Free.
- The identity module gets a `Guard` (`WithGuard`): `CreateApp`, `InviteMember` and `CreateAPIKey` call `RequireRoom` under `LockAccount`; the key check holds the lock in a transaction of its own around the issue. Rotation does not take a place. Accepting an invitation takes none (the invitation held it).
- The limit of an organisation is its override on the meter, else the entitlement of the plan version of its subscription in force, else the latest Free's; absent means unlimited.
- Plan changes: `ChangePlan` ends the subscription in force now (crediting an unused fee paid in advance) and starts the next, with the trial once; a failed start puts the old plan back; `same_plan` and `downgrade_blocked` guard a person's move, the operator's skips them.
- The sandbox retention sweep skips the platform account's tenant.

## As built: the trial-ending reminder

A trialing subscription is reminded **once per trial end**, `PAYRUDDER_TRIAL_REMINDER_DAYS` days before the trial ends (default 3, 1 to 30; one value for the whole service: the trial itself is the plan version's `trial_days` or the request's own, and a per-subscription reminder date would need a column on every subscription): the `subscription.trial_will_end` webhook event (`data.subscription` as the API returns it, with `trial_end_time`) and an e-mail to the customer. A trial shorter than the window is reminded at the first pass after it starts.

- Module `internal/trialnotices` (`model` the pure rules: the window, which trials are due, the event id and the fact; `command` the job). `command.Job` runs in every plans-svc replica every `PAYRUDDER_TRIAL_REMINDER_EVERY` (default 10 minutes). It lists, on the **system pool**, across tenants, the trialing subscriptions that Payrudder runs (`owner` is not `provider`: a provider reminds its own customers), are open-ended and not set to cancel at the period end, whose `trial_ends_at` is after now and within the window and for which no notice exists (`SystemListTrialsEndingSoon`, oldest end first, 200 at a time; a full batch runs again at once).
- Each candidate is then handled in its **own tenant's transaction on the app pool** (`pgxx.InTenantTx`): the subscription row is locked and re-checked (`Trial.Remindable`), then `subscription_trial_notices` gets its row, the event is queued through `webhooks/command.EmitSubscription` (only if an endpoint wants the type) and the `TrialWillEnd` fact is written to the `outbox` (topic `subscription.trial-will-end.v1`, relayed like `plans.entitlements.v1`, so the mail waits in the outbox while `PAYRUDDER_KAFKA_BROKERS` is unset). The notice, the event and the fact are in the same transaction: all three or none.
- **Idempotency.** Migration `00057`: `subscription_trial_notices (tenant_id, customer_id, trial_ends_at, subscription_id, notified_at)`, primary key `(tenant_id, customer_id, trial_ends_at)`, forced RLS, `plans_app` SELECT and INSERT, `plans_system` SELECT (the list) and DELETE (the sandbox reset). The insert is `ON CONFLICT DO NOTHING` and comes after the row lock, so a second pass, or a second replica, finds the notice and does nothing; a replica that loses the race waits on the lock and then also finds it. The key is the customer and the **end** of the trial, not the subscription: a plan change during a trial makes a new subscription with the same trial end and is not reminded again; a trial that is extended or started afresh has another end and is reminded again.
- A trial that is already over when the job comes (it was down) is not reminded; a paused, cancelled, scheduled-to-end or provider-run trial is not either.
- `subscription_trial_notices` is customer-side history (it has a foreign key to `customers`), so a **sandbox reset deletes it** with the subscriptions (`internal/sandbox`, `Tables`, `ResetTrialNotices`), unlike configuration such as `tenant_mail_integrations`, which is kept.
- The `TrialWillEnd` fact (`proto/payrudder/events/v1/trial_will_end.proto`): `event_id` is `uuidv5(tenant|customer|trial_end)`, so the notifier's dedup absorbs a repeated publish. notifier turns it into the mail (`trial_will_end.v1`, [workers](workers.md#notifier)).

## As built: renaming and deleting an app

`RenameMyApp` and `DeleteMyApp` on `IdentityService` (gateway: `PATCH` and `DELETE /app/v1/apps/{id}`, session routes; code in `identity/command/apps.go`, migration `00058`). Like every `My*` call they take the person from the session and find the app through the person's membership, so a stranger's app is "not found".

- **Rename**: owners and admins; a viewer gets `403 identity.account.forbidden`. The name is trimmed, 1 to 200 characters. It is the name of the app **and of its tenants** (the sandbox one with the suffix ` (sandbox)`), which is what the seller line of a *new* invoice says; invoices already issued keep the name they were issued under. The slug (the address) does not change. Audited (`app_audit`, action `renamed`, from and to).
- **Delete**: **owners only** (an admin manages an app but does not end it; `403` otherwise). **Rule: deleting archives, always, and never erases.** An app's tenants hold customers, subscriptions, invoices, payments and usage that accounting and the retention rules keep, and splitting "only sandbox data" from "has live money data" would add a second path whose mistake destroys a billing record; one rule cannot. The archive (`apps.archived_at`, `archived_by`) happens in one transaction under a row lock on the app: the slug is rewritten to `<slug>-archived-<12 hex of the id>` (a new app may take the old name and slug), every active key of both tenants is revoked, and an `app_audit` row (action `archived`, the number of keys revoked) is written; the gateway's key cache is evicted after the commit.
- **What "archived" means**: gone for the account. The account's lists, `GetMyApp`, `ResolveMyTenant` (so no session can act on the tenant), keys, go-live, promote, sandbox reset and rename all answer "not found" (`GetAppForUser` and `GetTenantForUser` leave archived apps out); the app no longer counts against the plan's app quota or the 20-app limit. **Ingest is refused**: its keys are revoked and evicted, `Issue` refuses a key for a tenant of an archived app (`plans.api_key.app_archived`, also for the operator), and the trigger `api_keys_app_not_archived` refuses an active key whatever the code does. The operator's `ListApps` still lists it, with `archive_time`.
- **What does not stop**: billing-svc and payments-worker keep working on what exists (`ListTenants` still walks the tenants): subscriptions renew, a customer who owes is invoiced and charged, an issued invoice can be paid, webhooks of the live tenant are delivered. Deleting does not cancel subscriptions; the console says so and the owner cancels them first when billing should stop. There is no un-delete in the console (the operator can clear `archived_at` in the database).
- Tests: `domain` unit test of the name; `-tags integration` `AppRenameAndDelete` (roles, tenant names, lists, keys revoked and evicted, no new key by member or operator, the trigger, customers and subscriptions kept, operator view, audit rows, slug reuse, quota); the gateway's `TestAppRenameAndDeleteRoutes`; `e2e-mock/app-manage.spec.ts`. The routes are session routes, so they need no entry in `pkg/apikey` `RequiredPermission` (that table classifies the routes an API key can reach; `TestEveryTenantRouteNeedsAKnownPermission` is unaffected).
