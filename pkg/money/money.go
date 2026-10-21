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

// currencies is the supported list: every active ISO 4217 currency that is money (funds codes, precious metals and test codes are left out).
// Extend deliberately: the exponent drives every invoice amount. Bulgaria joined the euro on 2026-01-01, so BGN is not listed.
var currencies = map[string]Currency{
	"AED": {"AED", 2},
	"AFN": {"AFN", 2},
	"ALL": {"ALL", 2},
	"AMD": {"AMD", 2},
	"AOA": {"AOA", 2},
	"ARS": {"ARS", 2},
	"AUD": {"AUD", 2},
	"AWG": {"AWG", 2},
	"AZN": {"AZN", 2},
	"BAM": {"BAM", 2},
	"BBD": {"BBD", 2},
	"BDT": {"BDT", 2},
	"BHD": {"BHD", 3},
	"BIF": {"BIF", 0},
	"BMD": {"BMD", 2},
	"BND": {"BND", 2},
	"BOB": {"BOB", 2},
	"BRL": {"BRL", 2},
	"BSD": {"BSD", 2},
	"BTN": {"BTN", 2},
	"BWP": {"BWP", 2},
	"BYN": {"BYN", 2},
	"BZD": {"BZD", 2},
	"CAD": {"CAD", 2},
	"CDF": {"CDF", 2},
	"CHF": {"CHF", 2},
	"CLP": {"CLP", 0},
	"CNY": {"CNY", 2},
	"COP": {"COP", 2},
	"CRC": {"CRC", 2},
	"CUP": {"CUP", 2},
	"CVE": {"CVE", 2},
	"CZK": {"CZK", 2},
	"DJF": {"DJF", 0},
	"DKK": {"DKK", 2},
	"DOP": {"DOP", 2},
	"DZD": {"DZD", 2},
	"EGP": {"EGP", 2},
	"ERN": {"ERN", 2},
	"ETB": {"ETB", 2},
	"EUR": {"EUR", 2},
	"FJD": {"FJD", 2},
	"FKP": {"FKP", 2},
	"GBP": {"GBP", 2},
	"GEL": {"GEL", 2},
	"GHS": {"GHS", 2},
	"GIP": {"GIP", 2},
	"GMD": {"GMD", 2},
	"GNF": {"GNF", 0},
	"GTQ": {"GTQ", 2},
	"GYD": {"GYD", 2},
	"HKD": {"HKD", 2},
	"HNL": {"HNL", 2},
	"HTG": {"HTG", 2},
	"HUF": {"HUF", 2},
	"IDR": {"IDR", 2},
	"ILS": {"ILS", 2},
	"INR": {"INR", 2},
	"IQD": {"IQD", 3},
	"IRR": {"IRR", 2},
	"ISK": {"ISK", 0},
	"JMD": {"JMD", 2},
	"JOD": {"JOD", 3},
	"JPY": {"JPY", 0},
	"KES": {"KES", 2},
	"KGS": {"KGS", 2},
	"KHR": {"KHR", 2},
	"KMF": {"KMF", 0},
	"KPW": {"KPW", 2},
	"KRW": {"KRW", 0},
	"KWD": {"KWD", 3},
	"KYD": {"KYD", 2},
	"KZT": {"KZT", 2},
	"LAK": {"LAK", 2},
	"LBP": {"LBP", 2},
	"LKR": {"LKR", 2},
	"LRD": {"LRD", 2},
	"LSL": {"LSL", 2},
	"LYD": {"LYD", 3},
	"MAD": {"MAD", 2},
	"MDL": {"MDL", 2},
	"MGA": {"MGA", 2},
	"MKD": {"MKD", 2},
	"MMK": {"MMK", 2},
	"MNT": {"MNT", 2},
	"MOP": {"MOP", 2},
	"MRU": {"MRU", 2},
	"MUR": {"MUR", 2},
	"MVR": {"MVR", 2},
	"MWK": {"MWK", 2},
	"MXN": {"MXN", 2},
	"MYR": {"MYR", 2},
	"MZN": {"MZN", 2},
	"NAD": {"NAD", 2},
	"NGN": {"NGN", 2},
	"NIO": {"NIO", 2},
	"NOK": {"NOK", 2},
	"NPR": {"NPR", 2},
	"NZD": {"NZD", 2},
	"OMR": {"OMR", 3},
	"PAB": {"PAB", 2},
	"PEN": {"PEN", 2},
	"PGK": {"PGK", 2},
	"PHP": {"PHP", 2},
	"PKR": {"PKR", 2},
	"PLN": {"PLN", 2},
	"PYG": {"PYG", 0},
	"QAR": {"QAR", 2},
	"RON": {"RON", 2},
	"RSD": {"RSD", 2},
	"RUB": {"RUB", 2},
	"RWF": {"RWF", 0},
	"SAR": {"SAR", 2},
	"SBD": {"SBD", 2},
	"SCR": {"SCR", 2},
	"SDG": {"SDG", 2},
	"SEK": {"SEK", 2},
	"SGD": {"SGD", 2},
	"SHP": {"SHP", 2},
	"SLE": {"SLE", 2},
	"SOS": {"SOS", 2},
	"SRD": {"SRD", 2},
	"SSP": {"SSP", 2},
	"STN": {"STN", 2},
	"SYP": {"SYP", 2},
	"SZL": {"SZL", 2},
	"THB": {"THB", 2},
	"TJS": {"TJS", 2},
	"TMT": {"TMT", 2},
	"TND": {"TND", 3},
	"TOP": {"TOP", 2},
	"TRY": {"TRY", 2},
	"TTD": {"TTD", 2},
	"TWD": {"TWD", 2},
	"TZS": {"TZS", 2},
	"UAH": {"UAH", 2},
	"UGX": {"UGX", 0},
	"USD": {"USD", 2},
	"UYU": {"UYU", 2},
	"UZS": {"UZS", 2},
	"VES": {"VES", 2},
	"VND": {"VND", 0},
	"VUV": {"VUV", 0},
	"WST": {"WST", 2},
	"XAF": {"XAF", 0},
	"XCD": {"XCD", 2},
	"XCG": {"XCG", 2},
	"XOF": {"XOF", 0},
	"XPF": {"XPF", 0},
	"YER": {"YER", 2},
	"ZAR": {"ZAR", 2},
	"ZMW": {"ZMW", 2},
	"ZWG": {"ZWG", 2},
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
