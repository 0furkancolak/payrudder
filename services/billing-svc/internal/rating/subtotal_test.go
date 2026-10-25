package rating

import "testing"

// The subtotal is what the lines add up to before tax: the tax lines are not in it, credits and discounts are.
func TestSubtotalLeavesOutTax(t *testing.T) {
	lines := []Line{
		{Kind: KindBaseFee, AmountMinor: 1000},
		{Kind: KindUsage, AmountMinor: 250},
		{Kind: KindDiscount, AmountMinor: -100},
		{Kind: KindTax, AmountMinor: 230},
		{Kind: KindCredit, AmountMinor: -50},
	}
	if got := Subtotal(lines); got != 1100 {
		t.Errorf("Subtotal = %d, want 1100", got)
	}
	if got := Subtotal(nil); got != 0 {
		t.Errorf("Subtotal(nil) = %d, want 0", got)
	}
}
