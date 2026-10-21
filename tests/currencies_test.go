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
