package query

import (
	"strings"
	"testing"
)

func TestCurrencyExponentFallsBackToTwo(t *testing.T) {
	for code, want := range map[string]int32{"USD": 2, "JPY": 0, "KWD": 3, "NOPE": 2, "": 2} {
		if got := CurrencyExponent(code); got != want {
			t.Errorf("CurrencyExponent(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestValidTokenIsTwentyToOneHundredCharacters(t *testing.T) {
	for n, want := range map[int]bool{0: false, 19: false, 20: true, 43: true, 100: true, 101: false} {
		if got := validToken(strings.Repeat("a", n)); got != want {
			t.Errorf("validToken(%d characters) = %v, want %v", n, got, want)
		}
	}
}

// An invoice whose payment waits for the customer to confirm it (3-D Secure) is open and payable on its page; one whose charge is in flight, or pending a bank transfer, is not.
func TestStateOfAnInvoiceWaitingForItsCustomer(t *testing.T) {
	for _, c := range []struct {
		invoice, saga, state string
		payable              bool
	}{
		{"open", "PAYMENT_ACTION_REQUIRED", "open", true},
		{"open", "AWAITING_PAYMENT", "open", true},
		{"open", "PAYMENT_DECLINED", "failed", true},
		{"open", "PAYMENT_PENDING", "processing", false},
		{"open", "PAYMENT_REQUESTED", "processing", false},
		{"paid", "PAYMENT_ACTION_REQUIRED", "paid", false},
		{"void", "PAYMENT_ACTION_REQUIRED", "void", false},
	} {
		if state, payable := State(c.invoice, c.saga); state != c.state || payable != c.payable {
			t.Errorf("State(%s, %s) = %s, %v; want %s, %v", c.invoice, c.saga, state, payable, c.state, c.payable)
		}
	}
}
