package inputs

import "github.com/0furkancolak/payrudder/pkg/money"

// CurrencyExponent is the number of decimals of a currency, or false when billing does not support it.
func CurrencyExponent(code string) (int, bool) {
	cur, ok := money.LookupCurrency(code)
	return cur.Exponent, ok
}
