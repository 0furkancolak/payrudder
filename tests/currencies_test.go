package tests

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/0furkancolak/quotify/pkg/money"
)

// The console offers the currencies in web/admin/src/shared/lib/currencies.ts; the backend accepts the ones in pkg/money. A currency the console
// offers and the backend refuses would fail late, and one the backend accepts but the console lacks could not be chosen, so the two lists must agree.
func TestConsoleCurrencyListMatchesBackend(t *testing.T) {
	src, err := os.ReadFile("../web/admin/src/shared/lib/currencies.ts")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)export const CURRENCIES = \[(.*?)\] as const`).FindSubmatch(src)
	if block == nil {
		t.Fatal("CURRENCIES not found in currencies.ts")
	}
	var ts []string
	for _, m := range regexp.MustCompile(`"([A-Z]{3})"`).FindAllSubmatch(block[1], -1) {
		ts = append(ts, string(m[1]))
	}
	slices.Sort(ts)
	if got := money.SupportedCurrencies(); !slices.Equal(got, ts) {
		t.Fatalf("console list differs from pkg/money:\nconsole %v\nbackend %v", ts, got)
	}
}

// The console formats and parses amounts with its own exponent table, not Intl's: ICU gives HUF, IDR, COP, IQD and others a different number of
// fraction digits than pkg/money, which would show those amounts 100 to 1000 times off. The two tables must agree.
func TestConsoleCurrencyExponentsMatchBackend(t *testing.T) {
	src, err := os.ReadFile("../web/admin/src/shared/lib/currencies.ts")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)export const CURRENCY_EXPONENTS[^{]*\{(.*?)\};`).FindSubmatch(src)
	if block == nil {
		t.Fatal("CURRENCY_EXPONENTS not found in currencies.ts")
	}
	ts := map[string]int{}
	for _, m := range regexp.MustCompile(`([A-Z]{3}): (\d)`).FindAllSubmatch(block[1], -1) {
		ts[string(m[1])] = int(m[2][0] - '0')
	}
	for _, code := range money.SupportedCurrencies() {
		c, _ := money.LookupCurrency(code)
		if got, ok := ts[code]; !ok || got != c.Exponent {
			t.Errorf("%s: console exponent %d (present=%v), backend %d", code, got, ok, c.Exponent)
		}
	}
	if len(ts) != len(money.SupportedCurrencies()) {
		t.Errorf("console table has %d currencies, backend %d", len(ts), len(money.SupportedCurrencies()))
	}
}
