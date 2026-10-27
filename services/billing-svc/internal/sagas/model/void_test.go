package model

import "testing"

// A void gives back what is left of the invoice: the hand-recorded money as a credit balance, the rest through the card, credit notes off the card part first.
func TestVoidSplitSendsHandMoneyToTheBalanceAndTheRestToTheCard(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		total, credited, byHand int64
		wantCard, wantHand      int64
	}{
		{"card only", 1000, 0, 0, 1000, 0},
		{"card, credited in part", 1000, 400, 0, 600, 0},
		{"card, credited in full", 1000, 1000, 0, 0, 0},
		{"hand in full", 1000, 0, 1000, 0, 1000},
		{"hand in part, card the rest", 1000, 0, 400, 600, 400},
		{"hand in part, a note takes the card part first", 1000, 300, 400, 300, 400},
		{"hand in full, a note comes off the hand part", 1000, 300, 1000, 0, 700},
	} {
		card, hand := VoidSplit(tc.total, tc.credited, tc.byHand)
		if card != tc.wantCard || hand != tc.wantHand {
			t.Errorf("%s: VoidSplit = card %d, hand %d; want %d, %d", tc.name, card, hand, tc.wantCard, tc.wantHand)
		}
		c := Ctx{TotalMinor: tc.total, CreditedMinor: tc.credited, ByHandMinor: tc.byHand, Paid: true}
		if PaidWithMoney.holds(c) != (tc.wantCard > 0) || NothingToRefund.holds(c) != (tc.wantCard == 0) {
			t.Errorf("%s: guards refund=%v nothing=%v with %d for the card", tc.name, PaidWithMoney.holds(c), NothingToRefund.holds(c), tc.wantCard)
		}
	}
}
