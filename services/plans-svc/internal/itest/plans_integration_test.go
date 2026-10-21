//go:build integration

package itest

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/quotify/gen/go/quotify/events/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/apikey"
	"github.com/0furkancolak/quotify/pkg/outbox"
	"github.com/0furkancolak/quotify/pkg/pgxx"
)

// Tables with tenant_id that are deliberately not RLS'd (docs/05-data-model.md#plans_db): operator-only api_keys,
// and outbox, which only the system role reads.
var rlsAllowlist = []string{"api_keys", "outbox"}

// TestPlansSvc runs every scenario against one shared Postgres container.
func TestPlansSvc(t *testing.T) {
	top = t
	t.Run("RLSIsolationEveryTable", testRLSIsolationEveryTable)
	t.Run("CreateAccountChoicesAndImmutability", testCreateAccountChoicesAndImmutability)
	t.Run("SandboxOnlyDeployment", testSandboxOnlyDeployment)
	t.Run("APIKeysFollowEnvironmentAndRevocationEvicts", testAPIKeysFollowEnvironmentAndRevocationEvicts)
	t.Run("SandboxPaymentMethods", testSandboxPaymentMethods)
	t.Run("Subscriptions", testSubscriptions)
	t.Run("SubscriptionLifecycle", testSubscriptionLifecycle)
	t.Run("SubscriptionChanges", testSubscriptionChanges)
	t.Run("Coupons", testCoupons)
	t.Run("CancelCredit", testCancelCredit)
	t.Run("Webhooks", testWebhooks)
	t.Run("Apps", testApps)
	t.Run("Identity", testIdentity)
	t.Run("EmailVerificationAndReset", testEmailVerificationAndReset)
	t.Run("MembersAndInvitations", testMembersAndInvitations)
	t.Run("Addresses", testAddresses)
	t.Run("TaxRates", testTaxRates)
	t.Run("MailSettings", testMailSettings)
	t.Run("CurrencyFeesAndCustomerBilling", testCurrencyFeesAndCustomerBilling)
	t.Run("ManyCurrenciesAndZones", testManyCurrenciesAndZones)
	t.Run("DashboardFacts", testDashboardFacts)
	t.Run("EntitlementOutbox", testEntitlementOutbox)
	t.Run("PlanVersionsAndValidation", testPlanVersionsAndValidation)
}

func code(t *testing.T, err error) codes.Code {
	t.Helper()
	return status.Code(err)
}

// seed creates one row in every tenant-owned table for the tenant, through the public API.
func seed(t *testing.T, e *env, tenantID, suffix string) {
	t.Helper()
	ctx := as(tenantID)
	must := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_" + suffix, Name: "Ada " + suffix, Email: "ada@example.com"}))
	must(e.plans.CreateMeter(ctx, &plansv1.CreateMeterRequest{Code: "gb_hours", Aggregation: plansv1.Aggregation_AGGREGATION_SUM,
		ValueProperty: "gb_h", QuantityScale: 3, UnitLabel: "GB-h"}))
	must(e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "pro", Name: "Pro"}))
	must(e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "pro", BaseFeeMinor: 9900,
		Fees: []*plansv1.CurrencyFee{{Currency: "USD", BaseFeeMinor: 300}},
		CurrencyPrices: []*plansv1.CurrencyPrices{{Currency: "USD", Prices: []*plansv1.Price{
			{MeterCode: "gb_hours", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: "0.4"}}}}}},
		Prices:       []*plansv1.Price{{MeterCode: "gb_hours", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: "12.5"}}}},
		Entitlements: []*plansv1.Entitlement{{MeterCode: "gb_hours", Limit: "100.5"}}}))
	must(e.plans.SetRoutingRules(ctx, &plansv1.SetRoutingRulesRequest{Rules: []*plansv1.RoutingRule{{Country: "TR", Currency: "TRY", Provider: "iyzico"}}}))
	must(e.plans.SetMailSettings(ctx, &plansv1.SetMailSettingsRequest{Categories: []*plansv1.MailCategorySetting{{Category: "usage_alerts", Enabled: false}}}))
	must(e.plans.SetTaxRates(ctx, &plansv1.SetTaxRatesRequest{Rates: []*plansv1.TaxRate{{Country: "TR", Name: "KDV", RateBps: 2000}}}))
	must(e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: &plansv1.Coupon{Code: "SEED", PercentBps: 100, Duration: plansv1.CouponDuration_COUPON_DURATION_ONCE}}))
	must(e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: "https://example.com/hook"}))
	must(e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_" + suffix, PlanCode: "pro", CouponCode: "SEED"}))
	// provider_migrations is written by StartProviderMigration, which would move the fixture's subscription; an aborted row stands in.
	if err := pgxx.InTenantTx(context.Background(), e.app, uuid.MustParse(tenantID), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO provider_migrations (tenant_id, id, subscription_id, to_provider, state)
			SELECT app_tenant_id(), gen_random_uuid(), id, 'iyzico', 'aborted' FROM subscriptions LIMIT 1`)
		if err != nil {
			return err
		}
		// Likewise a scheduled change and a billed adjustment, which ChangeSubscription would make by moving the subscription.
		if _, err = tx.Exec(context.Background(), `INSERT INTO subscription_changes (tenant_id, id, subscription_id, plan_version_id, interval, effective_at, state)
			SELECT app_tenant_id(), gen_random_uuid(), id, plan_version_id, 'month', now(), 'cancelled' FROM subscriptions LIMIT 1`); err != nil {
			return err
		}
		_, err = tx.Exec(context.Background(), `INSERT INTO subscription_adjustments (tenant_id, id, subscription_id, old_plan_code, from_at, to_at, credit_minor, charge_minor, billed_at)
			SELECT app_tenant_id(), gen_random_uuid(), id, 'pro', now(), now(), 1, 2, now() FROM subscriptions LIMIT 1`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func testRLSIsolationEveryTable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.account(t, "Tenant A")[plansv1.Environment_ENVIRONMENT_LIVE]
	b := e.account(t, "Tenant B")[plansv1.Environment_ENVIRONMENT_LIVE]
	seed(t, e, a.GetId(), "a")
	seed(t, e, b.GetId(), "b")
	aID, bID := uuid.MustParse(a.GetId()), uuid.MustParse(b.GetId())

	if err := pgxx.VerifyForcedRLS(ctx, e.system, rlsAllowlist); err != nil {
		t.Fatalf("forced RLS check: %v", err)
	}
	tables, err := pgxx.TenantTables(ctx, e.system, rlsAllowlist)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) < 7 {
		t.Fatalf("only %d RLS tables found: %v", len(tables), tables)
	}

	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			ident := pgx.Identifier{table}.Sanitize()
			// The fixture must cover every table, so a new table without seed data fails here.
			var bRow []byte
			err := e.system.QueryRow(ctx, fmt.Sprintf(`SELECT row_to_json(t)::text FROM %s t WHERE tenant_id = $1 LIMIT 1`, ident), bID).Scan(&bRow)
			if err != nil {
				t.Fatalf("no seed row for tenant B (extend seed()): %v", err)
			}

			err = pgxx.InTenantTx(ctx, e.app, aID, func(tx pgx.Tx) error {
				var n int
				if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE tenant_id = $1`, ident), bID).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					return fmt.Errorf("SELECT: tenant A sees %d rows of tenant B", n)
				}
				var own int
				if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s`, ident)).Scan(&own); err != nil {
					return err
				}
				if own == 0 {
					return fmt.Errorf("SELECT: tenant A doesn't see its own rows")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}

			// UPDATE of B's rows either touches nothing (RLS) or isn't granted at all.
			err = pgxx.InTenantTx(ctx, e.app, aID, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET tenant_id = tenant_id WHERE tenant_id = $1`, ident), bID)
				if err != nil {
					return err
				}
				if tag.RowsAffected() != 0 {
					return fmt.Errorf("UPDATE: changed %d rows of tenant B", tag.RowsAffected())
				}
				return nil
			})
			if err != nil && pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
				t.Fatal(err)
			}

			// INSERT of a row belonging to B must be rejected (WITH CHECK, or no INSERT grant).
			err = pgxx.InTenantTx(ctx, e.app, aID, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %[1]s SELECT * FROM json_populate_record(NULL::%[1]s, $1::json)`, ident), string(bRow))
				return err
			})
			if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
				t.Fatalf("INSERT as tenant A of a tenant B row: want 42501, got %v", err)
			}

			// No tenant context at all: zero rows, no error.
			var n int
			if err := e.app.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s`, ident)).Scan(&n); err != nil || n != 0 {
				t.Fatalf("without tenant context: %d rows, err %v", n, err)
			}
		})
	}

	t.Run("api calls are tenant scoped", func(t *testing.T) {
		_, err := e.plans.GetCustomer(as(a.GetId()), &plansv1.GetCustomerRequest{ExternalId: "cus_b"})
		if code(t, err) != codes.NotFound {
			t.Fatalf("tenant A reading tenant B's customer: %v", err)
		}
	})
}

func testCreateAccountChoicesAndImmutability(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	_, err := e.admin.CreateAccount(operator(), &plansv1.CreateAccountRequest{Name: "No choices"})
	if code(t, err) != codes.InvalidArgument {
		t.Fatalf("missing currency/timezone/grace: %v", err)
	}
	_, err = e.admin.CreateAccount(context.Background(), &plansv1.CreateAccountRequest{Name: "x"})
	if code(t, err) != codes.PermissionDenied {
		t.Fatalf("non-operator CreateAccount: %v", err)
	}

	tenants := e.account(t, "Immutable")
	if len(tenants) != 2 || tenants[plansv1.Environment_ENVIRONMENT_SANDBOX].GetCurrency() != "TRY" {
		t.Fatalf("tenants = %v", tenants)
	}
	live := tenants[plansv1.Environment_ENVIRONMENT_LIVE]
	_, err = e.system.Exec(ctx, `UPDATE tenants SET currency = 'USD' WHERE id = $1`, live.GetId())
	if pgxx.ErrCode(err) != pgxx.CodeRaiseException {
		t.Fatalf("currency update: %v", err)
	}
	_, err = e.system.Exec(ctx, `UPDATE tenants SET timezone = 'UTC' WHERE id = $1`, live.GetId())
	if pgxx.ErrCode(err) != pgxx.CodeRaiseException {
		t.Fatalf("timezone update: %v", err)
	}
	upd, err := e.admin.UpdateTenantSettings(operator(), &plansv1.UpdateTenantSettingsRequest{
		TenantId: live.GetId(), GracePeriod: durationpb.New(48 * time.Hour)})
	if err != nil || upd.GetTenant().GetGracePeriod().AsDuration() != 48*time.Hour || upd.GetTenant().GetTier() != plansv1.TenantTier_TENANT_TIER_STANDARD {
		t.Fatalf("grace update = %v, %v", upd, err)
	}
}

func testSandboxOnlyDeployment(t *testing.T) {
	e := newEnv(t, "sandbox")
	tenants := e.account(t, "Demo")
	if len(tenants) != 1 || tenants[plansv1.Environment_ENVIRONMENT_SANDBOX] == nil {
		t.Fatalf("sandbox-only deployment created %v", tenants)
	}
}

func testAPIKeysFollowEnvironmentAndRevocationEvicts(t *testing.T) {
	e := newEnv(t)
	tenants := e.account(t, "Keys")
	for env, want := range map[plansv1.Environment]apikey.Env{
		plansv1.Environment_ENVIRONMENT_SANDBOX: apikey.EnvTest, plansv1.Environment_ENVIRONMENT_LIVE: apikey.EnvLive,
	} {
		resp, err := e.admin.CreateApiKey(operator(), &plansv1.CreateApiKeyRequest{TenantId: tenants[env].GetId()})
		if err != nil {
			t.Fatal(err)
		}
		k, err := apikey.Parse(resp.GetPlaintext())
		if err != nil || k.Env != want {
			t.Fatalf("%v tenant got key %q (%v)", env, resp.GetPlaintext(), err)
		}
		res, err := e.admin.ResolveApiKey(context.Background(), &plansv1.ResolveApiKeyRequest{Prefix: k.Prefix})
		if err != nil || res.GetTenantId() != tenants[env].GetId() || !k.Matches(res.GetKeyHash()) ||
			res.GetEnvironment() != env || res.GetTimezone() != "Europe/Istanbul" || res.GetStatus() != plansv1.ApiKey_STATUS_ACTIVE {
			t.Fatalf("ResolveApiKey = %v, %v", res, err)
		}

		if _, err := e.admin.RevokeApiKey(operator(), &plansv1.RevokeApiKeyRequest{ApiKeyId: resp.GetApiKey().GetId()}); err != nil {
			t.Fatal(err)
		}
		res, err = e.admin.ResolveApiKey(context.Background(), &plansv1.ResolveApiKeyRequest{Prefix: k.Prefix})
		if err != nil || res.GetStatus() != plansv1.ApiKey_STATUS_REVOKED {
			t.Fatalf("after revoke: %v, %v", res, err)
		}
		e.evicted.mu.Lock()
		evicted := e.evicted.prefixes[len(e.evicted.prefixes)-1]
		e.evicted.mu.Unlock()
		if evicted != k.Prefix {
			t.Fatalf("evicted %q, want %q", evicted, k.Prefix)
		}
	}
	_, err := e.admin.ResolveApiKey(context.Background(), &plansv1.ResolveApiKeyRequest{Prefix: "ZZZZZZZZ"})
	if code(t, err) != codes.NotFound {
		t.Fatalf("unknown prefix: %v", err)
	}
}

func testSandboxPaymentMethods(t *testing.T) {
	e := newEnv(t)
	sb := e.account(t, "Sandbox PM")[plansv1.Environment_ENVIRONMENT_SANDBOX]
	ctx := as(sb.GetId())
	_, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "c1", Name: "C", Email: "c@example.com", PaymentMethod: "tok_visa"})
	if code(t, err) != codes.InvalidArgument {
		t.Fatalf("sandbox customer with real token: %v", err)
	}
	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "c1", Name: "C", Email: "c@example.com"}); err != nil {
		t.Fatal(err)
	}
	resp, err := e.plans.SetPaymentMethod(ctx, &plansv1.SetPaymentMethodRequest{ExternalId: "c1", PaymentMethod: "pm_sandbox_timeout_committed"})
	if err != nil || resp.GetCustomer().GetPaymentMethod() != "pm_sandbox_timeout_committed" {
		t.Fatalf("SetPaymentMethod = %v, %v", resp, err)
	}
}

func testSubscriptions(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Subs")[plansv1.Environment_ENVIRONMENT_LIVE]
	seed(t, e, live.GetId(), "s")
	ctx := as(live.GetId())

	_, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_s", PlanCode: "pro"})
	if code(t, err) != codes.AlreadyExists {
		t.Fatalf("overlapping subscription: %v", err)
	}

	list, err := e.plans.ListSubscriptionsForPeriod(ctx, &plansv1.ListSubscriptionsForPeriodRequest{
		PeriodStart: timestamppb.New(time.Now().Add(-time.Hour)), PeriodEnd: timestamppb.New(time.Now().Add(time.Hour))})
	if err != nil {
		t.Fatal(err)
	}
	var subs []*plansv1.Subscription
	for {
		r, err := list.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, r.GetSubscription())
	}
	if len(subs) != 1 || subs[0].GetCustomerExternalId() != "cus_s" || subs[0].GetEndTime() != nil {
		t.Fatalf("subscriptions = %v", subs)
	}

	end := time.Now().Add(time.Minute).Truncate(time.Microsecond)
	c, err := e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: subs[0].GetId(), EndTime: timestamppb.New(end)})
	if err != nil || !c.GetSubscription().GetEndTime().AsTime().Equal(end) {
		t.Fatalf("cancel = %v, %v", c, err)
	}
	_, err = e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: subs[0].GetId()})
	if code(t, err) != codes.FailedPrecondition {
		t.Fatalf("second cancel: %v", err)
	}
	// After the end, a new subscription for the same customer may start.
	if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_s", PlanCode: "pro",
		StartTime: timestamppb.New(end)}); err != nil {
		t.Fatalf("resubscribe after end: %v", err)
	}
}

func testPlanVersionsAndValidation(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Plans")[plansv1.Environment_ENVIRONMENT_LIVE]
	seed(t, e, live.GetId(), "p")
	ctx := as(live.GetId())

	_, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "pro", Prices: []*plansv1.Price{{
		MeterCode: "gb_hours", Model: &plansv1.Price_Tiered{Tiered: &plansv1.TieredGraduatedPrice{Tiers: []*plansv1.PriceTier{
			{UpTo: "100", UnitPrice: "1"}, {UpTo: "50", UnitPrice: "1"}, {UnitPrice: "1"}}}}}}})
	st := status.Convert(err)
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("non-ascending tiers: %v", err)
	}
	var field string
	for _, d := range st.Details() {
		if br, ok := d.(*errdetails.BadRequest); ok {
			field = br.GetFieldViolations()[0].GetField()
		}
	}
	if field != "prices[0].tiered.tiers[1].up_to" {
		t.Fatalf("violation field = %q", field)
	}

	v2, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "pro", Prices: []*plansv1.Price{{
		MeterCode: "gb_hours", Model: &plansv1.Price_Tiered{Tiered: &plansv1.TieredGraduatedPrice{Tiers: []*plansv1.PriceTier{
			{UpTo: "10.5", UnitPrice: "0"}, {UnitPrice: "12.5"}}}}}}})
	if err != nil || v2.GetPlanVersion().GetVersion() != 2 {
		t.Fatalf("publish v2 = %v, %v", v2, err)
	}
	got, err := e.plans.GetPlanVersion(ctx, &plansv1.GetPlanVersionRequest{PlanCode: "pro"})
	if err != nil || got.GetPlanVersion().GetVersion() != 2 || got.GetPlanVersion().GetPrices()[0].GetTiered().GetTiers()[0].GetUpTo() != "10.500" {
		t.Fatalf("latest = %v, %v", got, err)
	}
	v1, err := e.plans.GetPlanVersion(ctx, &plansv1.GetPlanVersionRequest{PlanCode: "pro", Version: 1})
	if err != nil || v1.GetPlanVersion().GetEntitlements()[0].GetLimit() != "100.500" {
		t.Fatalf("v1 = %v, %v", v1, err)
	}

	// Append-only: the app role cannot change a published version.
	err = pgxx.InTenantTx(context.Background(), e.app, uuid.MustParse(live.GetId()), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `UPDATE plan_versions SET base_fee_minor = 0`)
		return err
	})
	if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
		t.Fatalf("UPDATE plan_versions as app: %v", err)
	}

	m2, err := e.plans.CreateMeter(ctx, &plansv1.CreateMeterRequest{Code: "gb_hours", Aggregation: plansv1.Aggregation_AGGREGATION_SUM,
		ValueProperty: "gb_h", QuantityScale: 3, UnitLabel: "GB-h", Filter: map[string]*plansv1.FilterValues{"region": {Values: []string{"eu"}}}})
	if err != nil || m2.GetMeter().GetVersion() != 2 {
		t.Fatalf("meter v2 = %v, %v", m2, err)
	}
	_, err = e.plans.CreateMeter(ctx, &plansv1.CreateMeterRequest{Code: "gb_hours", Aggregation: plansv1.Aggregation_AGGREGATION_SUM,
		ValueProperty: "gb_h", QuantityScale: 2, UnitLabel: "GB-h"})
	if code(t, err) != codes.InvalidArgument {
		t.Fatalf("scale change: %v", err)
	}
}

type captureSink struct{ rows []outbox.Row }

func (c *captureSink) Publish(_ context.Context, rows []outbox.Row) error {
	c.rows = append(c.rows, rows...)
	return nil
}

// Subscribing and cancelling write plans.entitlements.v1 rows in the same transaction, and the relay (running as
// plans_system) can claim, publish and delete them.
func testEntitlementOutbox(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Outbox")[plansv1.Environment_ENVIRONMENT_LIVE]
	tenantID := live.GetId()
	seed(t, e, tenantID, "o") // creates cus_o, plan "pro" with a 100.5 gb_hours limit, and a subscription
	ctx := as(tenantID)

	relayAll := func() []outbox.Row {
		sink := &captureSink{}
		relay, err := outbox.NewRelay(e.system, sink, outbox.Config{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for {
			n, err := relay.RelayOnce(context.Background())
			if err != nil {
				t.Fatalf("relay: %v", err)
			}
			if n == 0 {
				// The database is shared with the other subtests: keep this tenant's rows.
				var mine []outbox.Row
				for _, r := range sink.rows {
					if r.TenantID == tenantID {
						mine = append(mine, r)
					}
				}
				return mine
			}
		}
	}
	rows := relayAll()
	if len(rows) != 1 || rows[0].Topic != "plans.entitlements.v1" || rows[0].TenantID != tenantID {
		t.Fatalf("after subscribing: %+v", rows)
	}
	var st eventsv1.EntitlementState
	if err := proto.Unmarshal(rows[0].Payload, &st); err != nil {
		t.Fatal(err)
	}
	custID := st.GetCustomerId()
	if rows[0].PartitionKey != tenantID+"/"+custID+"/gb_hours" || st.GetLimitUnits() != 100500 || st.GetQuantityScale() != 3 ||
		st.GetValidUntil() != nil || st.GetValidFrom() == nil || len(st.GetAlertThresholdsPct()) == 0 {
		t.Fatalf("entitlement state = %v (key %s)", &st, rows[0].PartitionKey)
	}
	if again := relayAll(); len(again) != 0 {
		t.Fatalf("relayed rows were not deleted: %v", again)
	}

	// Cancelling in the future publishes the state with its end; cancelling now publishes a tombstone.
	list, err := e.plans.ListSubscriptionsForPeriod(ctx, &plansv1.ListSubscriptionsForPeriodRequest{
		PeriodStart: timestamppb.New(time.Now().Add(-time.Hour)), PeriodEnd: timestamppb.New(time.Now().Add(time.Hour))})
	if err != nil {
		t.Fatal(err)
	}
	r, err := list.Recv()
	if err != nil {
		t.Fatal(err)
	}
	subID := r.GetSubscription().GetId()
	end := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	if _, err := e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: subID, EndTime: timestamppb.New(end)}); err != nil {
		t.Fatal(err)
	}
	rows = relayAll()
	if len(rows) != 1 || rows[0].Payload == nil {
		t.Fatalf("future cancel: %+v", rows)
	}
	st.Reset()
	if err := proto.Unmarshal(rows[0].Payload, &st); err != nil || !st.GetValidUntil().AsTime().Equal(end) {
		t.Fatalf("future cancel state = %v, %v", &st, err)
	}

	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_o2", Name: "O2", Email: "o2@example.com"}); err != nil {
		t.Fatal(err)
	}
	sub, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_o2", PlanCode: "pro",
		StartTime: timestamppb.New(time.Now().Add(-time.Minute))})
	if err != nil {
		t.Fatal(err)
	}
	relayAll()
	if _, err := e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: sub.GetSubscription().GetId()}); err != nil {
		t.Fatal(err)
	}
	rows = relayAll()
	if len(rows) != 1 || rows[0].Payload != nil {
		t.Fatalf("cancel now must publish a tombstone: %+v", rows)
	}
}
