package money_test

import (
	"regexp"
	"slices"
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
	got := money.SupportedCurrencies()
	if got[0] != "AED" || len(got) < 150 {
		t.Fatalf("SupportedCurrencies = %v", got)
	}
	if !slices.IsSorted(got) {
		t.Fatal("SupportedCurrencies is not sorted")
	}
	for _, c := range got {
		if !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(c) {
			t.Errorf("bad code %q", c)
		}
	}
}

// The exponent drives every amount: the zero- and three-decimal currencies must be right, and codes that are not money must be refused.
func TestExponents(t *testing.T) {
	want := map[string]int{"USD": 2, "EUR": 2, "TRY": 2, "BRL": 2, "INR": 2, "JPY": 0, "KRW": 0, "VND": 0, "CLP": 0, "ISK": 0, "UGX": 0, "XOF": 0, "XAF": 0, "XPF": 0,
		"KWD": 3, "BHD": 3, "OMR": 3, "JOD": 3, "TND": 3, "IQD": 3, "LYD": 3}
	for code, exp := range want {
		c, ok := money.LookupCurrency(code)
		if !ok || c.Exponent != exp {
			t.Errorf("%s = %+v, %v; want exponent %d", code, c, ok, exp)
		}
	}
	for _, code := range []string{"XXX", "XAU", "XAG", "XDR", "XTS", "BOV", "CHE", "USN", "BGN", "HRK", "usd", ""} {
		if _, ok := money.LookupCurrency(code); ok {
			t.Errorf("%q accepted", code)
		}
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
