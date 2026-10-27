package command

import "testing"

// TestCarriesCoupon is the one rule for the coupon of a replaced subscription. coupons.Redeem only puts a coupon on a
// subscription Payrudder invoices in advance, so only a successor of that kind can hold it.
func TestCarriesCoupon(t *testing.T) {
	for _, c := range []struct {
		name string
		next Successor
		want bool
	}{
		{"plan change, Payrudder-run, in advance", Successor{FeeTiming: "advance"}, true},
		{"explicit Payrudder owner", Successor{FeeTiming: "advance", Owner: "payrudder"}, true},
		{"billed in arrears", Successor{FeeTiming: "arrears"}, false},
		{"run by a provider", Successor{FeeTiming: "advance", Owner: "provider"}, false},
		{"run by a provider, in arrears (a sync)", Successor{FeeTiming: "arrears", Owner: "provider"}, false},
	} {
		if got := CarriesCoupon(c.next); got != c.want {
			t.Errorf("%s: CarriesCoupon = %v, want %v", c.name, got, c.want)
		}
	}
}
