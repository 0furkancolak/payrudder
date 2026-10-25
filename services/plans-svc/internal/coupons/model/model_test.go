package model

import (
	"testing"
	"time"
)

var now = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func percent(bps int32) Coupon { return Coupon{Active: true, PercentBPS: bps} }

func buy(amount int64) Purchase {
	return Purchase{Target: TargetPayment, AmountMinor: amount, Currency: "USD", CustomerKnown: true, Now: now}
}

func TestEvaluate(t *testing.T) {
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	tests := []struct {
		name    string
		c       Coupon
		code    *Code
		p       Purchase
		want    Result
		wantKey string
	}{
		{name: "a percentage is rounded half to even", c: percent(1000), p: buy(1005), want: Result{DiscountMinor: 100}},
		{name: "a half rounds to the even neighbour", c: percent(5000), p: buy(5), want: Result{DiscountMinor: 2}},
		{name: "an amount in the currency", c: Coupon{Active: true, AmountMinor: 500, Currency: "USD"}, p: buy(2000), want: Result{DiscountMinor: 500}},
		{name: "an amount never takes the total below zero", c: Coupon{Active: true, AmountMinor: 5000, Currency: "USD"}, p: buy(2000), want: Result{DiscountMinor: 2000}},
		{name: "a hundred percent is the whole price", c: percent(10000), p: buy(1234), want: Result{DiscountMinor: 1234}},
		{name: "an amount in another currency is refused", c: Coupon{Active: true, AmountMinor: 500, Currency: "EUR"}, p: buy(2000), wantKey: KeyCurrencyMismatch},
		{name: "bonus credits are a share of the credits sold", c: Coupon{Active: true, BonusBPS: 2500, AppliesTo: []string{TargetCreditPurchase}},
			p: Purchase{Target: TargetCreditPurchase, AmountMinor: 1000, Currency: "USD", Credits: 1000, Now: now}, want: Result{BonusCredits: 250}},
		{name: "bonus credits on a payment are refused", c: Coupon{Active: true, BonusBPS: 2500}, p: buy(1000), wantKey: KeyNotApplicable},
		{name: "inactive coupon", c: Coupon{PercentBPS: 100}, p: buy(1000), wantKey: KeyInactive},
		{name: "inactive code", c: percent(100), code: &Code{}, p: buy(1000), wantKey: KeyInactive},
		{name: "expired coupon", c: Coupon{Active: true, PercentBPS: 100, ExpiresAt: &past}, p: buy(1000), wantKey: KeyExpired},
		{name: "expired code", c: percent(100), code: &Code{Active: true, ExpiresAt: &past}, p: buy(1000), wantKey: KeyExpired},
		{name: "not yet expired", c: percent(1000), code: &Code{Active: true, ExpiresAt: &future}, p: buy(1000), want: Result{DiscountMinor: 100}},
		{name: "used up code", c: percent(100), code: &Code{Active: true, MaxRedeemed: ptr(int32(3)), Redeemed: 3}, p: buy(1000), wantKey: KeyExhausted},
		{name: "used up coupon", c: Coupon{Active: true, PercentBPS: 100, MaxRedeemed: ptr(int32(1)), Redeemed: 1}, p: buy(1000), wantKey: KeyExhausted},
		{name: "a coupon for subscriptions only", c: Coupon{Active: true, PercentBPS: 100, AppliesTo: []string{TargetSubscription}}, p: buy(1000), wantKey: KeyNotApplicable},
		{name: "allowed plans", c: percent(100), code: &Code{Active: true, AllowedPlans: []string{"pro"}},
			p: Purchase{Target: TargetSubscription, Currency: "USD", Item: "basic", CustomerKnown: true, Now: now}, wantKey: KeyNotApplicable},
		{name: "an allowed plan", c: percent(100), code: &Code{Active: true, AllowedPlans: []string{"pro"}},
			p: Purchase{Target: TargetSubscription, Currency: "USD", Item: "pro", CustomerKnown: true, Now: now}},
		{name: "allowed packs", c: percent(100), code: &Code{Active: true, AllowedPacks: []string{"p1"}},
			p: Purchase{Target: TargetCreditPurchase, AmountMinor: 1000, Currency: "USD", Item: "p2", Now: now}, wantKey: KeyNotApplicable},
		{name: "a pack restriction does not apply to a payment link", c: percent(100), code: &Code{Active: true, AllowedPacks: []string{"p1"}}, p: buy(1000), wantKey: KeyNotApplicable},
		{name: "allowed currencies", c: percent(100), code: &Code{Active: true, AllowedCurrencies: []string{"EUR"}}, p: buy(1000), wantKey: KeyCurrencyMismatch},
		{name: "below the minimum", c: percent(100), code: &Code{Active: true, MinAmountMinor: 5000, MinAmountCurrency: "USD"}, p: buy(4999), wantKey: KeyBelowMinimum},
		{name: "at the minimum", c: percent(1000), code: &Code{Active: true, MinAmountMinor: 5000, MinAmountCurrency: "USD"}, p: buy(5000), want: Result{DiscountMinor: 500}},
		{name: "a minimum in another currency", c: percent(100), code: &Code{Active: true, MinAmountMinor: 5000, MinAmountCurrency: "EUR"}, p: buy(9000), wantKey: KeyCurrencyMismatch},
		{name: "first purchase only, first", c: percent(1000), code: &Code{Active: true, FirstPurchaseOnly: true}, p: buy(1000), want: Result{DiscountMinor: 100}},
		{name: "first purchase only, not first", c: percent(1000), code: &Code{Active: true, FirstPurchaseOnly: true}, p: Purchase{Target: TargetPayment, AmountMinor: 1000,
			Currency: "USD", CustomerKnown: true, PriorPurchases: true, Now: now}, wantKey: KeyFirstPurchase},
		{name: "first purchase only needs a customer", c: percent(1000), code: &Code{Active: true, FirstPurchaseOnly: true}, p: Purchase{Target: TargetPayment, AmountMinor: 1000,
			Currency: "USD", Now: now}, wantKey: KeyCustomerRequired},
		{name: "one per customer, used", c: percent(1000), code: &Code{Active: true, OnePerCustomer: true}, p: Purchase{Target: TargetPayment, AmountMinor: 1000,
			Currency: "USD", CustomerKnown: true, CodeUses: 1, Now: now}, wantKey: KeyAlreadyUsed},
		{name: "one per customer, unused", c: percent(1000), code: &Code{Active: true, OnePerCustomer: true}, p: buy(1000), want: Result{DiscountMinor: 100}},
		{name: "a percentage of nothing takes nothing", c: percent(1000), p: buy(0), wantKey: KeyNotApplicable},
		{name: "a subscription is taken off the fees later", c: percent(1000), p: Purchase{Target: TargetSubscription, Currency: "USD", Now: now}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, rej := Evaluate(tt.c, tt.code, tt.p)
			switch {
			case tt.wantKey != "" && (rej == nil || rej.Key != tt.wantKey):
				t.Fatalf("rejection = %v, want %s", rej, tt.wantKey)
			case tt.wantKey == "" && rej != nil:
				t.Fatalf("unexpected rejection %v", rej)
			case got != tt.want:
				t.Fatalf("result = %+v, want %+v", got, tt.want)
			}
		})
	}
}
