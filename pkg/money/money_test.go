package money_test

import (
	"testing"

	"github.com/0furkancolak/quotify/pkg/money"
)

func TestCurrencies(t *testing.T) {
	if c, ok := money.LookupCurrency("JPY"); !ok || c.Exponent != 0 {
		t.Fatalf("JPY = %+v, %v", c, ok)
	}
	if _, ok := money.LookupCurrency("XXX"); ok {
		t.Fatal("unknown currency accepted")
	}
	if got := money.SupportedCurrencies(); got[0] != "CHF" || len(got) != 7 {
		t.Fatalf("SupportedCurrencies = %v", got)
	}
}

func TestValidateUnitPrice(t *testing.T) {
	for _, ok := range []string{"0", "0.002", "12.5", "100", "0.000000000001", "999999999999999999.999999999999"} {
		if err := money.ValidateUnitPrice(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-1", "01", "1.", ".5", "1e-3", "0.0000000000001", "1000000000000000000", "1,5"} {
		if err := money.ValidateUnitPrice(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
