//go:build integration

package itest

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
)

func testCoupons(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Coupons")[plansv1.Environment_ENVIRONMENT_LIVE] // TRY
	ctx := as(live.GetId())
	for _, p := range []struct {
		code string
		fee  int64
	}{{"cp_basic", 10000}, {"cp_pro", 30000}} {
		if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: p.code, Name: p.code}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: p.code, BaseFeeMinor: p.fee,
			Fees: []*plansv1.CurrencyFee{{Currency: "USD", BaseFeeMinor: p.fee / 100}}}); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	customer := func() string {
		n++
		ext := "cus_cp_" + string(rune('a'+n))
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		return ext
	}
	mk := func(c *plansv1.Coupon) {
		t.Helper()
		if _, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: c}); err != nil {
			t.Fatalf("%s: %v", c.GetCode(), err)
		}
	}
	once, repeating, forever := plansv1.CouponDuration_COUPON_DURATION_ONCE, plansv1.CouponDuration_COUPON_DURATION_REPEATING, plansv1.CouponDuration_COUPON_DURATION_FOREVER
	mk(&plansv1.Coupon{Code: "TEN", PercentBps: 1000, Duration: once})
	mk(&plansv1.Coupon{Code: "HALF2", PercentBps: 5000, Duration: repeating, DurationPeriods: 2})
	mk(&plansv1.Coupon{Code: "MINUS5", AmountMinor: 500, Currency: "TRY", Duration: forever})
	mk(&plansv1.Coupon{Code: "USD5", AmountMinor: 500, Currency: "USD", Duration: forever})
	mk(&plansv1.Coupon{Code: "ONLY1", PercentBps: 2500, Duration: once, MaxRedemptions: 1})

	t.Run("a coupon is validated when it is made", func(t *testing.T) {
		for name, c := range map[string]*plansv1.Coupon{
			"a bad code":                 {Code: "x", PercentBps: 100, Duration: once},
			"neither percent nor amount": {Code: "NOTHING", Duration: once},
			"both":                       {Code: "BOTH", PercentBps: 100, AmountMinor: 100, Currency: "TRY", Duration: once},
			"over 100 percent":           {Code: "OVER", PercentBps: 10001, Duration: once},
			"an amount without currency": {Code: "NOCUR", AmountMinor: 100, Duration: once},
			"repeating without periods":  {Code: "REP", PercentBps: 100, Duration: repeating},
			"periods for once":           {Code: "ONCE2", PercentBps: 100, Duration: once, DurationPeriods: 2},
			"no duration":                {Code: "NODUR", PercentBps: 100},
			"expired already":            {Code: "OLD", PercentBps: 100, Duration: once, ExpireTime: timestamppb.New(time.Now().Add(-time.Hour))},
		} {
			if _, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: c}); code(t, err) != codes.InvalidArgument {
				t.Errorf("%s: %v", name, err)
			}
		}
		if _, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: &plansv1.Coupon{Code: "TEN", PercentBps: 100, Duration: once}}); code(t, err) != codes.AlreadyExists {
			t.Errorf("a duplicate code: %v", err)
		}
		l, err := e.plans.ListCoupons(ctx, &plansv1.ListCouponsRequest{})
		if err != nil || len(l.GetCoupons()) != 5 {
			t.Errorf("list = %v, %v", l, err)
		}
	})

	feeDue := func(t *testing.T, id string, now time.Time) *plansv1.FeeDue {
		t.Helper()
		resp, err := e.plans.ListSubscriptionsFeeDue(ctx, &plansv1.ListSubscriptionsFeeDueRequest{Now: timestamppb.New(now), PageSize: 500})
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range resp.GetDue() {
			if d.GetSubscription().GetId() == id {
				return d
			}
		}
		return nil
	}
	advance := func(t *testing.T, id string, start time.Time) {
		t.Helper()
		if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: id, PeriodStart: timestamppb.New(start)}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a once coupon takes a percentage off the first fee only", func(t *testing.T) {
		start := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: customer(), PlanCode: "cp_basic", StartTime: timestamppb.New(start), CouponCode: "TEN"})
		if err != nil {
			t.Fatal(err)
		}
		sub := r.GetSubscription()
		if sub.GetCouponCode() != "TEN" {
			t.Fatalf("coupon on the subscription = %q", sub.GetCouponCode())
		}
		d := feeDue(t, sub.GetId(), time.Now())
		if d == nil || d.GetFeeMinor() != 10000 || d.GetDiscountMinor() != 1000 || d.GetCouponCode() != "TEN" {
			t.Fatalf("first fee = %v", d)
		}
		advance(t, sub.GetId(), start)
		advance(t, sub.GetId(), start) // twice: the coupon is used once
		second := feeDue(t, sub.GetId(), start.AddDate(0, 1, 0).Add(time.Second))
		if second == nil || second.GetDiscountMinor() != 0 || second.GetFeeMinor() != 10000 {
			t.Fatalf("second fee = %v; the coupon was for the first only", second)
		}
	})

	t.Run("a repeating coupon covers its periods", func(t *testing.T) {
		start := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: customer(), PlanCode: "cp_basic", StartTime: timestamppb.New(start), CouponCode: "HALF2"})
		if err != nil {
			t.Fatal(err)
		}
		id := r.GetSubscription().GetId()
		for i, want := range []int64{5000, 5000, 0} {
			ps := start.AddDate(0, i, 0)
			d := feeDue(t, id, ps.Add(time.Second))
			if d == nil || d.GetDiscountMinor() != want {
				t.Fatalf("period %d discount = %v; want %d", i+1, d, want)
			}
			advance(t, id, ps)
		}
	})

	t.Run("an amount coupon never takes more than the fee; a paused period uses no coupon", func(t *testing.T) {
		start := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: customer(), PlanCode: "cp_basic", StartTime: timestamppb.New(start), CouponCode: "MINUS5"})
		if err != nil {
			t.Fatal(err)
		}
		id := r.GetSubscription().GetId()
		if d := feeDue(t, id, time.Now()); d == nil || d.GetDiscountMinor() != 500 {
			t.Fatalf("amount discount = %v", d)
		}
		advance(t, id, start)
		if _, err := e.plans.RemoveCoupon(ctx, &plansv1.RemoveCouponRequest{SubscriptionId: id}); err != nil {
			t.Fatal(err)
		}
		if d := feeDue(t, id, start.AddDate(0, 1, 0).Add(time.Second)); d == nil || d.GetDiscountMinor() != 0 {
			t.Fatalf("after removing the coupon = %v", d)
		}
		if _, err := e.plans.RemoveCoupon(ctx, &plansv1.RemoveCouponRequest{SubscriptionId: id}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("removing nothing: %v", err)
		}
		// Redeemed later with ApplyCoupon, and a second one is refused.
		ap, err := e.plans.ApplyCoupon(ctx, &plansv1.ApplyCouponRequest{SubscriptionId: id, Code: "TEN"})
		if err != nil || ap.GetSubscription().GetCouponCode() != "TEN" {
			t.Fatalf("apply = %v, %v", ap, err)
		}
		if _, err := e.plans.ApplyCoupon(ctx, &plansv1.ApplyCouponRequest{SubscriptionId: id, Code: "MINUS5"}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("a second coupon: %v", err)
		}
	})

	t.Run("a coupon that cannot be redeemed is refused", func(t *testing.T) {
		c1 := customer()
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: c1, PlanCode: "cp_basic", CouponCode: "ONLY1"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: customer(), PlanCode: "cp_basic", CouponCode: "ONLY1"}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("a used up coupon: %v", err)
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: customer(), PlanCode: "cp_basic", CouponCode: "USD5"}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("an amount coupon in another currency: %v", err)
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: customer(), PlanCode: "cp_basic", CouponCode: "NOPE"}); code(t, err) != codes.NotFound {
			t.Errorf("an unknown coupon: %v", err)
		}
		d, err := e.plans.DeactivateCoupon(ctx, &plansv1.DeactivateCouponRequest{Code: "TEN"})
		if err != nil || d.GetCoupon().GetActive() {
			t.Fatalf("deactivate = %v, %v", d, err)
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: customer(), PlanCode: "cp_basic", CouponCode: "TEN"}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("an inactive coupon: %v", err)
		}
		// A failed redemption leaves no subscription behind.
		c2 := customer()
		_, _ = e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: c2, PlanCode: "cp_basic", CouponCode: "NOPE"})
		if l, _ := e.plans.ListSubscriptions(ctx, &plansv1.ListSubscriptionsRequest{CustomerExternalId: c2}); len(l.GetSubscriptions()) != 0 {
			t.Errorf("the refused coupon left %d subscription(s)", len(l.GetSubscriptions()))
		}
		// A subscription a provider renews has no coupon here.
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_cp_us", Name: "u", Email: "u@example.com", Country: "US", BillingCurrency: "USD"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_cp_us", PlanCode: "cp_basic", Provider: "paddle", CouponCode: "USD5"}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("a coupon on a Paddle subscription: %v", err)
		}
	})

	t.Run("a plan change carries the coupon, and its periods, over", func(t *testing.T) {
		start := time.Now().Add(-2 * time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: customer(), PlanCode: "cp_basic", StartTime: timestamppb.New(start), CouponCode: "HALF2"})
		if err != nil {
			t.Fatal(err)
		}
		old := r.GetSubscription()
		advance(t, old.GetId(), start) // period 1 used the coupon
		ch, err := e.plans.ChangeSubscription(ctx, &plansv1.ChangeSubscriptionRequest{SubscriptionId: old.GetId(), PlanCode: "cp_pro"})
		if err != nil {
			t.Fatal(err)
		}
		nu := ch.GetSubscription()
		if nu.GetCouponCode() != "HALF2" {
			t.Fatalf("the successor's coupon = %q", nu.GetCouponCode())
		}
		// The second fee of the new plan is discounted, the third is not.
		ps := start.AddDate(0, 1, 0)
		d := feeDue(t, nu.GetId(), ps.Add(time.Second))
		if d == nil || d.GetFeeMinor() != 30000 || d.GetDiscountMinor() != 15000 {
			t.Fatalf("second fee on the new plan = %v", d)
		}
		advance(t, nu.GetId(), ps)
		if d := feeDue(t, nu.GetId(), ps.AddDate(0, 1, 0).Add(time.Second)); d == nil || d.GetDiscountMinor() != 0 {
			t.Fatalf("third fee on the new plan = %v; the coupon had two periods", d)
		}
	})
}

func testTaxRates(t *testing.T) {
	e := newEnv(t)
	a := e.account(t, "Tax A")[plansv1.Environment_ENVIRONMENT_LIVE]
	b := e.account(t, "Tax B")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(a.GetId())
	if r, err := e.plans.GetTaxRates(ctx, &plansv1.GetTaxRatesRequest{}); err != nil || len(r.GetRates()) != 0 {
		t.Fatalf("new tenant = %v, %v", r, err)
	}
	set := []*plansv1.TaxRate{{Country: "DE", Name: "VAT", RateBps: 1900}, {Country: "TR", Name: "KDV", RateBps: 2000}, {Name: "Sales tax", RateBps: 0}}
	r, err := e.plans.SetTaxRates(ctx, &plansv1.SetTaxRatesRequest{Rates: set})
	if err != nil || len(r.GetRates()) != 3 {
		t.Fatalf("set = %v, %v", r, err)
	}
	// Saving replaces the list as a whole, and another tenant has its own.
	r, err = e.plans.SetTaxRates(ctx, &plansv1.SetTaxRatesRequest{Rates: set[:1]})
	if err != nil || len(r.GetRates()) != 1 || r.GetRates()[0].GetCountry() != "DE" {
		t.Fatalf("replace = %v, %v", r, err)
	}
	if other, _ := e.plans.GetTaxRates(as(b.GetId()), &plansv1.GetTaxRatesRequest{}); len(other.GetRates()) != 0 {
		t.Errorf("another tenant sees %v", other.GetRates())
	}
	for name, rates := range map[string][]*plansv1.TaxRate{
		"a bad country":     {{Country: "Germany", Name: "VAT", RateBps: 100}},
		"a duplicate":       {{Country: "DE", Name: "VAT", RateBps: 100}, {Country: "DE", Name: "VAT2", RateBps: 200}},
		"no name":           {{Country: "DE", RateBps: 100}},
		"over 100 percent":  {{Country: "DE", Name: "VAT", RateBps: 10001}},
		"a negative rate":   {{Country: "DE", Name: "VAT", RateBps: -1}},
		"a lower-case code": {{Country: "de", Name: "VAT", RateBps: 100}},
	} {
		if _, err := e.plans.SetTaxRates(ctx, &plansv1.SetTaxRatesRequest{Rates: rates}); code(t, err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if r, _ := e.plans.GetTaxRates(ctx, &plansv1.GetTaxRatesRequest{}); len(r.GetRates()) != 1 {
		t.Errorf("a refused save changed the rates: %v", r.GetRates())
	}
}
