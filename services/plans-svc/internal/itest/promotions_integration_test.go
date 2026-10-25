//go:build integration

package itest

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/migrations"
)

func testPromotions(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Promotions")[plansv1.Environment_ENVIRONMENT_LIVE] // TRY
	ctx := as(live.GetId())
	n := 0
	customer := func() string {
		n++
		ext := "cus_promo_" + string(rune('a'+n))
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		return ext
	}
	once := plansv1.CouponDuration_COUPON_DURATION_ONCE
	mk := func(c *plansv1.Coupon) *plansv1.Coupon {
		t.Helper()
		r, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: c})
		if err != nil {
			t.Fatalf("%s: %v", c.GetCode(), err)
		}
		return r.GetCoupon()
	}
	codesOf := func(coupon string, tpl *plansv1.PromotionCode, names ...string) []*plansv1.PromotionCode {
		t.Helper()
		r, err := e.plans.CreatePromotionCodes(ctx, &plansv1.CreatePromotionCodesRequest{CouponCode: coupon, Template: tpl, Codes: names})
		if err != nil {
			t.Fatalf("codes of %s: %v", coupon, err)
		}
		return r.GetCodes()
	}
	redeem := func(code, cust string, target plansv1.CouponTarget, amount int64) (*plansv1.RedeemPromotionResponse, error) {
		return e.plans.RedeemPromotion(ctx, &plansv1.RedeemPromotionRequest{Code: code, CustomerExternalId: cust, Target: target, TargetId: uuid.NewString(), AmountMinor: amount, Currency: "TRY"})
	}
	pay, credit := plansv1.CouponTarget_COUPON_TARGET_PAYMENT, plansv1.CouponTarget_COUPON_TARGET_CREDIT_PURCHASE
	wantKey := func(t *testing.T, what string, err error, c codes.Code, key string) {
		t.Helper()
		if code(t, err) != c || errkey.Of(err) != key {
			t.Errorf("%s: %v (key %q), want %s %s", what, err, errkey.Of(err), c, key)
		}
	}
	redemptions := func(t *testing.T, req *plansv1.ListCouponRedemptionsRequest) *plansv1.ListCouponRedemptionsResponse {
		t.Helper()
		r, err := e.plans.ListCouponRedemptions(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	codeRow := func(t *testing.T, text string) *plansv1.PromotionCode {
		t.Helper()
		r, err := e.plans.ListPromotionCodes(ctx, &plansv1.ListPromotionCodesRequest{Search: text})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range r.GetCodes() {
			if strings.EqualFold(c.GetCode(), text) {
				return c
			}
		}
		t.Fatalf("no promotion code %s", text)
		return nil
	}

	mk(&plansv1.Coupon{Code: "PCT10", PercentBps: 1000, Duration: once})
	mk(&plansv1.Coupon{Code: "MINUS50", AmountMinor: 5000, Currency: "TRY", Duration: once})
	mk(&plansv1.Coupon{Code: "EUR5", AmountMinor: 500, Currency: "EUR", Duration: once})
	mk(&plansv1.Coupon{Code: "BONUS25", BonusPercentBps: 2500})

	t.Run("a new coupon gets a promotion code with its own text; bonus coupons are for credit purchases", func(t *testing.T) {
		c := codeRow(t, "PCT10")
		if c.GetCouponCode() != "PCT10" || !c.GetActive() {
			t.Fatalf("default code = %v", c)
		}
		bonus, err := e.plans.ListCoupons(ctx, &plansv1.ListCouponsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		for _, cp := range bonus.GetCoupons() {
			if cp.GetCode() == "BONUS25" && (len(cp.GetAppliesTo()) != 1 || cp.GetAppliesTo()[0] != credit || cp.GetBonusPercentBps() != 2500 || cp.GetPromotionCodeCount() != 1) {
				t.Errorf("bonus coupon = %v", cp)
			}
		}
		for name, cp := range map[string]*plansv1.Coupon{
			"a bonus coupon for payments": {Code: "BADBONUS", BonusPercentBps: 100, AppliesTo: []plansv1.CouponTarget{plansv1.CouponTarget_COUPON_TARGET_PAYMENT}},
			"bonus and percent":           {Code: "BOTH2", BonusPercentBps: 100, PercentBps: 100, Duration: once},
		} {
			if _, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: cp}); code(t, err) != codes.InvalidArgument {
				t.Errorf("%s: %v", name, err)
			}
		}
	})

	t.Run("bulk-generated codes are random, unique, prefixed and share the rules", func(t *testing.T) {
		mk(&plansv1.Coupon{Code: "BULK", PercentBps: 500, Duration: once, WithoutDefaultCode: true})
		r, err := e.plans.CreatePromotionCodes(ctx, &plansv1.CreatePromotionCodesRequest{CouponCode: "BULK", Count: 120, Prefix: "SPR-",
			Template: &plansv1.PromotionCode{MaxRedemptions: 1, OnePerCustomer: true, AllowedCurrencies: []string{"TRY"}}})
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, c := range r.GetCodes() {
			if !strings.HasPrefix(c.GetCode(), "SPR-") || len(c.GetCode()) != 12 || seen[strings.ToUpper(c.GetCode())] {
				t.Fatalf("bad or repeated generated code %q", c.GetCode())
			}
			seen[strings.ToUpper(c.GetCode())] = true
			if strings.ContainsAny(c.GetCode()[4:], "0O1IL") || c.GetMaxRedemptions() != 1 || !c.GetOnePerCustomer() || len(c.GetAllowedCurrencies()) != 1 {
				t.Fatalf("generated code = %v", c)
			}
		}
		if len(seen) != 120 {
			t.Fatalf("made %d codes", len(seen))
		}
		l, err := e.plans.ListPromotionCodes(ctx, &plansv1.ListPromotionCodesRequest{CouponCode: "BULK", PageSize: 50})
		if err != nil || l.GetTotal() != 120 || len(l.GetCodes()) != 50 || l.GetNextPageToken() == "" {
			t.Fatalf("list = %d of %d, token %q, %v", len(l.GetCodes()), l.GetTotal(), l.GetNextPageToken(), err)
		}
		l2, err := e.plans.ListPromotionCodes(ctx, &plansv1.ListPromotionCodesRequest{CouponCode: "BULK", PageSize: 50, PageToken: l.GetNextPageToken()})
		if err != nil || len(l2.GetCodes()) != 50 || l2.GetCodes()[0].GetId() == l.GetCodes()[0].GetId() {
			t.Fatalf("second page = %v, %v", l2, err)
		}
		for name, req := range map[string]*plansv1.CreatePromotionCodesRequest{
			"codes and count":     {CouponCode: "BULK", Codes: []string{"AA1"}, Count: 2},
			"too many":            {CouponCode: "BULK", Count: 1001},
			"neither":             {CouponCode: "BULK"},
			"a bad code":          {CouponCode: "BULK", Codes: []string{"no spaces allowed"}},
			"a bad prefix":        {CouponCode: "BULK", Count: 1, Prefix: "no spaces"},
			"the same twice":      {CouponCode: "BULK", Codes: []string{"TWICE", "twice"}},
			"a past expiry":       {CouponCode: "BULK", Count: 1, Template: &plansv1.PromotionCode{ExpireTime: timestamppb.New(time.Now().Add(-time.Hour))}},
			"a minimum without":   {CouponCode: "BULK", Count: 1, Template: &plansv1.PromotionCode{MinAmountMinor: 100}},
			"an unknown currency": {CouponCode: "BULK", Count: 1, Template: &plansv1.PromotionCode{AllowedCurrencies: []string{"XXX"}}},
		} {
			if _, err := e.plans.CreatePromotionCodes(ctx, req); code(t, err) != codes.InvalidArgument {
				t.Errorf("%s: %v", name, err)
			}
		}
		if _, err := e.plans.CreatePromotionCodes(ctx, &plansv1.CreatePromotionCodesRequest{CouponCode: "NOPE", Count: 1}); code(t, err) != codes.NotFound {
			t.Errorf("an unknown coupon: %v", err)
		}
	})

	t.Run("a code is the same in any case, and typed in any case", func(t *testing.T) {
		codesOf("PCT10", nil, "Summer-26")
		if _, err := e.plans.CreatePromotionCodes(ctx, &plansv1.CreatePromotionCodesRequest{CouponCode: "MINUS50", Codes: []string{"SUMMER-26"}}); code(t, err) != codes.AlreadyExists ||
			errkey.Of(err) != "coupon.code_taken" {
			t.Errorf("same code in another case: %v", err)
		}
		r, err := redeem("sUmMeR-26", "", pay, 10000)
		if err != nil || r.GetDiscountMinor() != 1000 || r.GetTotalMinor() != 9000 || r.GetPromotionCode() != "Summer-26" || r.GetCouponCode() != "PCT10" {
			t.Fatalf("redeem = %v, %v", r, err)
		}
	})

	t.Run("a quote claims nothing; asking again for the same purchase is the same redemption", func(t *testing.T) {
		codesOf("PCT10", nil, "QUOTE10")
		before := codeRow(t, "QUOTE10").GetRedeemed()
		q, err := e.plans.RedeemPromotion(ctx, &plansv1.RedeemPromotionRequest{Code: "quote10", Target: pay, AmountMinor: 5000, Currency: "TRY", Preview: true})
		if err != nil || q.GetDiscountMinor() != 500 || q.GetRedemptionId() != "" || codeRow(t, "QUOTE10").GetRedeemed() != before {
			t.Fatalf("quote = %v, %v", q, err)
		}
		target := uuid.NewString()
		req := &plansv1.RedeemPromotionRequest{Code: "QUOTE10", Target: pay, TargetId: target, AmountMinor: 5000, Currency: "TRY"}
		a, err := e.plans.RedeemPromotion(ctx, req)
		if err != nil || a.GetRedemptionId() == "" {
			t.Fatal(a, err)
		}
		b, err := e.plans.RedeemPromotion(ctx, req)
		if err != nil || b.GetRedemptionId() != a.GetRedemptionId() || b.GetDiscountMinor() != 500 {
			t.Fatalf("replay = %v, %v", b, err)
		}
		if got := codeRow(t, "QUOTE10").GetRedeemed(); got != before+1 {
			t.Errorf("redeemed = %d after a replay, want %d", got, before+1)
		}
		_, err = e.plans.RedeemPromotion(ctx, &plansv1.RedeemPromotionRequest{Code: "MINUS50", Target: pay, TargetId: target, AmountMinor: 5000, Currency: "TRY"})
		wantKey(t, "another coupon on the same purchase", err, codes.FailedPrecondition, "coupon.already_applied")
	})

	t.Run("fifty checkouts race for the last redemptions: exactly the limit succeed, and the count and the ledger agree", func(t *testing.T) {
		mk(&plansv1.Coupon{Code: "RACE", PercentBps: 2000, Duration: once, WithoutDefaultCode: true})
		codesOf("RACE", &plansv1.PromotionCode{MaxRedemptions: 7}, "RACECODE")
		var ok, exhausted, other atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := redeem("RACECODE", "", pay, 1000)
				switch {
				case err == nil:
					ok.Add(1)
				case errkey.Of(err) == "coupon.exhausted":
					exhausted.Add(1)
				default:
					t.Errorf("unexpected: %v", err)
					other.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if ok.Load() != 7 || exhausted.Load() != 43 {
			t.Fatalf("succeeded %d, exhausted %d, other %d; want 7, 43, 0", ok.Load(), exhausted.Load(), other.Load())
		}
		if got := codeRow(t, "RACECODE").GetRedeemed(); got != 7 {
			t.Errorf("code redeemed = %d", got)
		}
		if got := redemptions(t, &plansv1.ListCouponRedemptionsRequest{PromotionCode: "RACECODE"}).GetTotal(); got != 7 {
			t.Errorf("ledger rows = %d", got)
		}
		l, _ := e.plans.ListCoupons(ctx, &plansv1.ListCouponsRequest{})
		for _, c := range l.GetCoupons() {
			if c.GetCode() == "RACE" && c.GetRedeemed() != 7 {
				t.Errorf("coupon redeemed = %d", c.GetRedeemed())
			}
		}
	})

	t.Run("the coupon's own limit counts all its codes", func(t *testing.T) {
		mk(&plansv1.Coupon{Code: "SHARED", PercentBps: 1000, Duration: once, MaxRedemptions: 3, WithoutDefaultCode: true})
		codesOf("SHARED", nil, "SHARED-A", "SHARED-B", "SHARED-C")
		var ok, exhausted atomic.Int32
		var wg sync.WaitGroup
		for i := range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := redeem([]string{"SHARED-A", "SHARED-B", "SHARED-C"}[i%3], "", pay, 1000)
				switch {
				case err == nil:
					ok.Add(1)
				case errkey.Of(err) == "coupon.exhausted":
					exhausted.Add(1)
				default:
					t.Errorf("unexpected: %v", err)
				}
			}()
		}
		wg.Wait()
		if ok.Load() != 3 || exhausted.Load() != 17 {
			t.Fatalf("succeeded %d, exhausted %d", ok.Load(), exhausted.Load())
		}
		if got := redemptions(t, &plansv1.ListCouponRedemptionsRequest{CouponCode: "SHARED"}).GetTotal(); got != 3 {
			t.Errorf("ledger rows = %d", got)
		}
	})

	t.Run("one use per customer, and first purchase only", func(t *testing.T) {
		codesOf("PCT10", &plansv1.PromotionCode{OnePerCustomer: true}, "ONCEEACH")
		codesOf("PCT10", &plansv1.PromotionCode{FirstPurchaseOnly: true}, "NEWBIE")
		ada, bob := customer(), customer()
		if _, err := redeem("ONCEEACH", ada, pay, 1000); err != nil {
			t.Fatal(err)
		}
		_, err := redeem("ONCEEACH", ada, pay, 1000)
		wantKey(t, "the same customer again", err, codes.FailedPrecondition, "coupon.already_used")
		if _, err := redeem("ONCEEACH", bob, pay, 1000); err != nil {
			t.Errorf("another customer: %v", err)
		}
		_, err = redeem("ONCEEACH", "", pay, 1000)
		wantKey(t, "a guest", err, codes.FailedPrecondition, "coupon.customer_required")

		// Parallel checkouts of one customer cannot both use a one-per-customer code.
		cara := customer()
		var ok atomic.Int32
		var wg sync.WaitGroup
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := redeem("ONCEEACH", cara, pay, 1000); err == nil {
					ok.Add(1)
				} else if errkey.Of(err) != "coupon.already_used" {
					t.Errorf("unexpected: %v", err)
				}
			}()
		}
		wg.Wait()
		if ok.Load() != 1 {
			t.Errorf("%d parallel checkouts of one customer used the code", ok.Load())
		}

		if _, err := e.plans.RedeemPromotion(ctx, &plansv1.RedeemPromotionRequest{Code: "NEWBIE", CustomerExternalId: ada, Target: pay, TargetId: uuid.NewString(), AmountMinor: 1000,
			Currency: "TRY", PriorPurchases: true}); code(t, err) != codes.FailedPrecondition || errkey.Of(err) != "coupon.first_purchase_only" {
			t.Errorf("a customer who bought before: %v", err)
		}
		if _, err := redeem("NEWBIE", ada, pay, 1000); err != nil {
			t.Errorf("a first purchase: %v", err)
		}
	})

	t.Run("restrictions: currency, minimum, packs, kind of purchase, expiry, deactivation", func(t *testing.T) {
		_, err := redeem("EUR5", "", pay, 10000)
		wantKey(t, "an amount coupon in another currency", err, codes.FailedPrecondition, "coupon.currency_mismatch")
		codesOf("PCT10", &plansv1.PromotionCode{AllowedCurrencies: []string{"EUR"}}, "EURONLY")
		_, err = redeem("EURONLY", "", pay, 10000)
		wantKey(t, "a code for other currencies", err, codes.FailedPrecondition, "coupon.currency_mismatch")
		codesOf("PCT10", &plansv1.PromotionCode{MinAmountMinor: 5000, MinAmountCurrency: "TRY"}, "BIGSPENDER")
		_, err = redeem("BIGSPENDER", "", pay, 4999)
		wantKey(t, "below the minimum", err, codes.FailedPrecondition, "coupon.below_minimum")
		if r, err := redeem("BIGSPENDER", "", pay, 5000); err != nil || r.GetDiscountMinor() != 500 {
			t.Errorf("at the minimum: %v, %v", r, err)
		}
		pack := uuid.NewString()
		codesOf("PCT10", &plansv1.PromotionCode{AllowedPacks: []string{pack}}, "ONEPACK")
		_, err = e.plans.RedeemPromotion(ctx, &plansv1.RedeemPromotionRequest{Code: "ONEPACK", Target: credit, TargetId: uuid.NewString(), AmountMinor: 1000, Currency: "TRY", Item: uuid.NewString()})
		wantKey(t, "another pack", err, codes.FailedPrecondition, "coupon.not_applicable")
		if r, err := e.plans.RedeemPromotion(ctx, &plansv1.RedeemPromotionRequest{Code: "ONEPACK", Target: credit, TargetId: uuid.NewString(), AmountMinor: 1000, Currency: "TRY", Item: pack}); err != nil || r.GetDiscountMinor() != 100 {
			t.Errorf("the pack: %v, %v", r, err)
		}
		mk(&plansv1.Coupon{Code: "SUBSONLY", PercentBps: 1000, Duration: once, AppliesTo: []plansv1.CouponTarget{plansv1.CouponTarget_COUPON_TARGET_SUBSCRIPTION}})
		_, err = redeem("SUBSONLY", "", pay, 1000)
		wantKey(t, "a subscription coupon on a payment", err, codes.FailedPrecondition, "coupon.not_applicable")
		_, err = redeem("BONUS25", "", pay, 1000)
		wantKey(t, "bonus credits on a payment", err, codes.FailedPrecondition, "coupon.not_applicable")
		_, err = redeem("NO-SUCH-CODE", "", pay, 1000)
		wantKey(t, "an unknown code", err, codes.NotFound, "coupon.not_found")
		_, err = redeem("PCT10", "", pay, 1000) // the coupon's own code is not a promotion code once there is none of that text
		if err != nil {
			t.Errorf("the default code of PCT10 is a promotion code: %v", err)
		}

		codesOf("PCT10", nil, "SOON")
		if err := pgxx.InTenantTx(context.Background(), e.app, uuid.MustParse(live.GetId()), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `UPDATE promotion_codes SET expires_at = now() - interval '1 minute' WHERE code = 'SOON'`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		_, err = redeem("SOON", "", pay, 1000)
		wantKey(t, "an expired code", err, codes.FailedPrecondition, "coupon.expired")

		codesOf("PCT10", nil, "STOPPED")
		if _, err := e.plans.DeactivatePromotionCode(ctx, &plansv1.DeactivatePromotionCodeRequest{Code: "stopped"}); err != nil {
			t.Fatal(err)
		}
		_, err = redeem("STOPPED", "", pay, 1000)
		wantKey(t, "a deactivated code", err, codes.FailedPrecondition, "coupon.inactive")
		if _, err := redeem("SUMMER-26", "", pay, 1000); err != nil {
			t.Errorf("the coupon's other codes still work: %v", err)
		}
		if _, err := e.plans.DeactivatePromotionCode(ctx, &plansv1.DeactivatePromotionCodeRequest{Code: "MISSING"}); code(t, err) != codes.NotFound {
			t.Errorf("deactivating a missing code: %v", err)
		}
	})

	t.Run("the discount never takes the total below zero, and rounds half to even", func(t *testing.T) {
		mk(&plansv1.Coupon{Code: "FULL", PercentBps: 10000, Duration: once})
		if r, err := redeem("FULL", "", pay, 777); err != nil || r.GetDiscountMinor() != 777 || r.GetTotalMinor() != 0 {
			t.Errorf("full = %v, %v", r, err)
		}
		if r, err := redeem("MINUS50", "", pay, 3000); err != nil || r.GetDiscountMinor() != 3000 || r.GetTotalMinor() != 0 {
			t.Errorf("amount over the price = %v, %v", r, err)
		}
		if r, err := redeem("PCT10", "", pay, 25); err != nil || r.GetDiscountMinor() != 2 { // 2.5 -> 2
			t.Errorf("half to even = %v, %v", r, err)
		}
	})

	t.Run("bonus credits are a share of the credits sold", func(t *testing.T) {
		r, err := e.plans.RedeemPromotion(ctx, &plansv1.RedeemPromotionRequest{Code: "BONUS25", Target: credit, TargetId: uuid.NewString(), AmountMinor: 1000, Currency: "TRY", Credits: 1001})
		if err != nil || r.GetBonusCredits() != 250 || r.GetDiscountMinor() != 0 || r.GetTotalMinor() != 1000 {
			t.Fatalf("bonus = %v, %v", r, err)
		}
		l := redemptions(t, &plansv1.ListCouponRedemptionsRequest{CouponCode: "BONUS25"})
		if len(l.GetRedemptions()) != 1 || l.GetRedemptions()[0].GetBonusCredits() != 250 || l.GetRedemptions()[0].GetTarget() != credit {
			t.Errorf("ledger = %v", l)
		}
	})

	t.Run("a reversal gives the redemption back only for a code that says so", func(t *testing.T) {
		codesOf("PCT10", &plansv1.PromotionCode{MaxRedemptions: 1}, "KEEPS")
		codesOf("PCT10", &plansv1.PromotionCode{MaxRedemptions: 1, RestoreOnReversal: true}, "RESTORES")
		for _, c := range []struct {
			code    string
			restore bool
		}{{"KEEPS", false}, {"RESTORES", true}} {
			id := uuid.NewString()
			if _, err := e.plans.RedeemPromotion(ctx, &plansv1.RedeemPromotionRequest{Code: c.code, Target: credit, TargetId: id, AmountMinor: 1000, Currency: "TRY"}); err != nil {
				t.Fatal(err)
			}
			if _, err := redeem(c.code, "", credit, 1000); errkey.Of(err) != "coupon.exhausted" {
				t.Fatalf("%s before the reversal: %v", c.code, err)
			}
			r, err := e.plans.ReversePromotion(ctx, &plansv1.ReversePromotionRequest{Target: credit, TargetId: id})
			if err != nil || !r.GetReversed() {
				t.Fatalf("reverse = %v, %v", r, err)
			}
			if r, err := e.plans.ReversePromotion(ctx, &plansv1.ReversePromotionRequest{Target: credit, TargetId: id}); err != nil || r.GetReversed() {
				t.Errorf("reversing twice = %v, %v", r, err)
			}
			_, err = redeem(c.code, "", credit, 1000)
			if c.restore && err != nil {
				t.Errorf("%s after a reversal: %v", c.code, err)
			}
			if !c.restore {
				wantKey(t, c.code+" after a reversal", err, codes.FailedPrecondition, "coupon.exhausted")
			}
			rows := redemptions(t, &plansv1.ListCouponRedemptionsRequest{PromotionCode: c.code})
			reversed := 0
			for _, row := range rows.GetRedemptions() {
				if row.GetReverseTime() != nil {
					reversed++
				}
			}
			if reversed != 1 {
				t.Errorf("%s: %d reversed rows of %d", c.code, reversed, len(rows.GetRedemptions()))
			}
		}
		if r, err := e.plans.ReversePromotion(ctx, &plansv1.ReversePromotionRequest{Target: pay, TargetId: uuid.NewString()}); err != nil || r.GetReversed() {
			t.Errorf("reversing a purchase that used nothing = %v, %v", r, err)
		}
	})

	t.Run("a coupon waiting for a customer is used by their next checkout without a code", func(t *testing.T) {
		mk(&plansv1.Coupon{Code: "WELCOME", PercentBps: 3000, Duration: once, WithoutDefaultCode: true})
		cust := customer()
		grant := &plansv1.GrantCustomerCouponRequest{CustomerExternalId: cust, CouponCode: "WELCOME", Source: "affiliate", SourceRef: "ref-1"}
		if r, err := e.plans.GrantCustomerCoupon(ctx, grant); err != nil || !r.GetGranted() {
			t.Fatal(r, err)
		}
		if r, err := e.plans.GrantCustomerCoupon(ctx, grant); err != nil || r.GetGranted() {
			t.Errorf("the same grant again = %v, %v", r, err)
		}
		if r, err := redeem("", customer(), pay, 1000); err != nil || r.GetApplied() {
			t.Errorf("another customer = %v, %v", r, err)
		}
		if r, err := redeem("", cust, pay, 1000); err != nil || !r.GetApplied() || r.GetDiscountMinor() != 300 || r.GetCouponCode() != "WELCOME" || r.GetPromotionCode() != "" {
			t.Fatalf("the grant = %v, %v", r, err)
		}
		if r, err := redeem("", cust, pay, 1000); err != nil || r.GetApplied() {
			t.Errorf("the grant is used once: %v, %v", r, err)
		}
		if _, err := e.plans.GrantCustomerCoupon(ctx, &plansv1.GrantCustomerCouponRequest{CustomerExternalId: cust, CouponCode: "NOPE", Source: "affiliate", SourceRef: "r"}); code(t, err) != codes.NotFound {
			t.Errorf("an unknown coupon: %v", err)
		}
	})

	t.Run("a subscription redemption is in the ledger and grows with each fee it is taken off", func(t *testing.T) {
		if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "promo_basic", Name: "promo_basic"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "promo_basic", BaseFeeMinor: 10000}); err != nil {
			t.Fatal(err)
		}
		mk(&plansv1.Coupon{Code: "SUBTWO", PercentBps: 1000, Duration: plansv1.CouponDuration_COUPON_DURATION_REPEATING, DurationPeriods: 2})
		codesOf("SUBTWO", &plansv1.PromotionCode{AllowedPlans: []string{"other_plan"}}, "OTHERPLAN")
		cust := customer()
		start := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: cust, PlanCode: "promo_basic", StartTime: timestamppb.New(start), CouponCode: "OTHERPLAN"}); code(t, err) != codes.FailedPrecondition ||
			errkey.Of(err) != "coupon.not_applicable" {
			t.Fatalf("a code for another plan: %v", err)
		}
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: cust, PlanCode: "promo_basic", StartTime: timestamppb.New(start), CouponCode: "subtwo"})
		if err != nil || r.GetSubscription().GetCouponCode() != "SUBTWO" {
			t.Fatalf("create = %v, %v", r, err)
		}
		if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: r.GetSubscription().GetId(), PeriodStart: timestamppb.New(start)}); err != nil {
			t.Fatal(err)
		}
		l := redemptions(t, &plansv1.ListCouponRedemptionsRequest{CouponCode: "SUBTWO", CustomerExternalId: cust})
		if len(l.GetRedemptions()) != 1 || l.GetRedemptions()[0].GetDiscountMinor() != 1000 || l.GetRedemptions()[0].GetTarget() != plansv1.CouponTarget_COUPON_TARGET_SUBSCRIPTION ||
			l.GetRedemptions()[0].GetTargetId() != r.GetSubscription().GetId() {
			t.Fatalf("ledger = %v", l)
		}
	})
}

// testPromotionsMigration runs the migration back and forward on data a pre-ledger database would hold: coupons with a redemption count, some on subscriptions and
// one taken off again (whose count is not given back). It must come back with a promotion code per coupon and a ledger that agrees with the counts.
func testPromotionsMigration(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "PromoMigration")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	tenantID := uuid.MustParse(live.GetId())
	once := plansv1.CouponDuration_COUPON_DURATION_ONCE
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "mig_plan", Name: "mig"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "mig_plan", BaseFeeMinor: 10000}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*plansv1.Coupon{{Code: "MIGA", PercentBps: 1000, Duration: once, MaxRedemptions: 10}, {Code: "MIGB", AmountMinor: 100, Currency: "TRY", Duration: once}} {
		if _, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: c}); err != nil {
			t.Fatal(err)
		}
	}
	var subs []string
	for i, code := range []string{"MIGA", "MIGA", "MIGB"} {
		ext := "cus_mig_" + string(rune('a'+i))
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: ext, PlanCode: "mig_plan", CouponCode: code})
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, r.GetSubscription().GetId())
	}
	// The second MIGA subscription loses its coupon: the redemption count of MIGA stays 2.
	if _, err := e.plans.RemoveCoupon(ctx, &plansv1.RemoveCouponRequest{SubscriptionId: subs[1]}); err != nil {
		t.Fatal(err)
	}

	query := func(sql string, args ...any) (out [][]any) {
		t.Helper()
		if err := pgxx.InTenantTx(context.Background(), e.app, tenantID, func(tx pgx.Tx) error {
			rows, err := tx.Query(context.Background(), sql, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				v, err := rows.Values()
				if err != nil {
					return err
				}
				out = append(out, v)
			}
			return rows.Err()
		}); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return out
	}
	count := func(sql string) int64 {
		t.Helper()
		return query(sql)[0][0].(int64)
	}
	counters := func() map[string]int64 {
		out := map[string]int64{}
		for _, r := range query(`SELECT code, redeemed::bigint FROM coupons ORDER BY code`) {
			out[r[0].(string)] = r[1].(int64)
		}
		return out
	}
	consistent := func(t *testing.T, when string) {
		t.Helper()
		if n := count(`SELECT count(*) FROM coupons c WHERE NOT EXISTS (SELECT 1 FROM promotion_codes p WHERE p.coupon_id = c.id AND p.code = c.code)`); n != 0 {
			t.Errorf("%s: %d coupons without a promotion code of their text", when, n)
		}
		// Every coupon's count is its ledger: the rows that took a place and did not give it back.
		if n := count(`SELECT count(*) FROM coupons c WHERE c.redeemed <> (SELECT count(*) FROM coupon_redemptions r WHERE r.coupon_id = c.id AND (r.reversed_at IS NULL OR NOT r.slot_returned))`); n != 0 {
			t.Errorf("%s: %d coupons whose count and ledger disagree", when, n)
		}
		if n := count(`SELECT count(*) FROM subscription_coupons WHERE redemption_id IS NULL`); n != 0 {
			t.Errorf("%s: %d subscription coupons without a redemption", when, n)
		}
	}
	before := counters()
	if before["MIGA"] != 2 || before["MIGB"] != 1 {
		t.Fatalf("counters before = %v", before)
	}
	consistent(t, "before")

	ctxb := context.Background()
	// The affiliate program (47) refers to a coupon by the key this migration adds, so it goes down first and comes back last.
	if err := pgxx.MigrateVersion(ctxb, e.db.Migrator, migrations.FS, 47, false); err != nil {
		t.Fatal(err)
	}
	if err := pgxx.MigrateVersion(ctxb, e.db.Migrator, migrations.FS, 30, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // whatever happens, leave the schema migrated for the tests that follow
		_ = pgxx.MigrateVersion(ctxb, e.db.Migrator, migrations.FS, 30, true)
		_ = pgxx.MigrateVersion(ctxb, e.db.Migrator, migrations.FS, 47, true)
	})
	if n := count(`SELECT count(*) FROM information_schema.tables WHERE table_name IN ('promotion_codes', 'coupon_redemptions', 'customer_coupons')`); n != 0 {
		t.Fatalf("down left %d new tables", n)
	}
	if n := count(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'coupons' AND column_name IN ('id', 'applies_to', 'bonus_percent_bps')`); n != 0 {
		t.Fatalf("down left %d new columns", n)
	}
	if got := counters(); got["MIGA"] != before["MIGA"] || got["MIGB"] != before["MIGB"] {
		t.Fatalf("counters after down = %v", got)
	}

	for round := 1; round <= 2; round++ { // up, then down and up again: the migration repeats on its own result
		if err := pgxx.MigrateVersion(ctxb, e.db.Migrator, migrations.FS, 30, true); err != nil {
			t.Fatal(err)
		}
		consistent(t, "after up")
		if got := counters(); got["MIGA"] != before["MIGA"] || got["MIGB"] != before["MIGB"] {
			t.Fatalf("counters after up = %v", got)
		}
		// MIGA: one subscription holds it, and one redemption was taken off again and is legacy now.
		if n := count(`SELECT count(*) FROM coupon_redemptions r JOIN coupons c ON c.id = r.coupon_id WHERE c.code = 'MIGA' AND r.target_kind = 'subscription'`); n != 1 {
			t.Errorf("MIGA subscription rows = %d", n)
		}
		if n := count(`SELECT count(*) FROM coupon_redemptions r JOIN coupons c ON c.id = r.coupon_id WHERE c.code = 'MIGA' AND r.target_kind = 'legacy'`); n != int64(round) {
			t.Errorf("MIGA legacy rows = %d, want %d", n, round)
		}
		// The coupon works through its new promotion code, and the old way through ApplyCoupon.
		cust := "cus_mig_after_" + string(rune('0'+round))
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: cust, Name: cust, Email: cust + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		if r, err := e.plans.RedeemPromotion(ctx, &plansv1.RedeemPromotionRequest{Code: "miga", Target: plansv1.CouponTarget_COUPON_TARGET_PAYMENT, TargetId: uuid.NewString(),
			AmountMinor: 1000, Currency: "TRY"}); err != nil || r.GetDiscountMinor() != 100 {
			t.Errorf("redeem after up = %v, %v", r, err)
		}
		consistent(t, "after a redemption")
		before = counters()
		if round == 1 {
			if err := pgxx.MigrateVersion(ctxb, e.db.Migrator, migrations.FS, 30, false); err != nil {
				t.Fatal(err)
			}
			if got := counters(); got["MIGA"] != before["MIGA"] {
				t.Fatalf("counters after a second down = %v, want %v", got, before)
			}
		}
	}
}
