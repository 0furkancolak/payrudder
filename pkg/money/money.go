// Package money holds the currency table and decimal unit-price validation (ADR 0010).
// Amounts are int64 minor units; unit prices are decimal strings stored as numeric(30,12).
// Rating arithmetic (apd, half-even rounding) is added with billing in Phase 05.
package money

import (
	"errors"
	"regexp"
	"slices"
)

// Currency is a supported ISO 4217 currency with its minor-unit exponent.
type Currency struct {
	Code     string
	Exponent int
}

// currencies is the supported list for CreateAccount. Extend deliberately: the exponent drives every invoice amount.
var currencies = map[string]Currency{
	"TRY": {"TRY", 2},
	"USD": {"USD", 2},
	"EUR": {"EUR", 2},
	"GBP": {"GBP", 2},
	"CHF": {"CHF", 2},
	"JPY": {"JPY", 0},
	"KWD": {"KWD", 3},
}

// LookupCurrency returns a supported currency.
func LookupCurrency(code string) (Currency, bool) {
	c, ok := currencies[code]
	return c, ok
}

// SupportedCurrencies returns the supported codes, sorted.
func SupportedCurrencies() []string {
	codes := make([]string, 0, len(currencies))
	for c := range currencies {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	return codes
}

// ErrInvalidPrice: not a non-negative decimal that fits numeric(30,12).
var ErrInvalidPrice = errors.New("money: unit price must be a non-negative decimal with at most 18 integer and 12 fractional digits")

var priceRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})(\.[0-9]{1,12})?$`)

// ValidateUnitPrice checks a unit price string such as "0.002" (minor units per meter unit).
func ValidateUnitPrice(s string) error {
	if !priceRe.MatchString(s) {
		return ErrInvalidPrice
	}
	return nil
}
