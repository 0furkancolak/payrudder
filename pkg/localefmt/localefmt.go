// Package localefmt writes numbers, money and dates the way the readers of a language expect them (the conventions of CLDR), for the six languages of the customer-facing
// documents and pages: en, tr, de, fr, es, ar. Go has no Intl, so the conventions are a table here, and the invoice PDF and the hosted pages read the same one.
package localefmt

import (
	"fmt"
	"strings"
	"time"

	"github.com/0furkancolak/payrudder/pkg/money"
)

// Format is one language's way of writing numbers and dates.
type Format struct {
	Code string
	// Decimal and Group separate the fraction and the thousands; MinGroup is the fewest digits before the first group separator (Spanish writes 1234,56 but 12.345,67).
	Decimal, Group string
	MinGroup       int
	// Minus is written in front of a negative number. Arabic puts a left-to-right mark before it so that the sign stays on the left of the digits in right-to-left text.
	Minus string
	// MoneyGap separates a number from its currency code.
	MoneyGap string
	// DateFmt takes the day, the month name and the year; MonthYearFmt the month name and the year.
	DateFmt, MonthYearFmt string
	Months                [12]string
}

// For is the format of a language; English for a language this package does not know.
func For(code string) *Format {
	if f, ok := formats[code]; ok {
		return f
	}
	return formats["en"]
}

// Languages lists the codes For knows.
func Languages() []string { return []string{"en", "tr", "de", "fr", "es", "ar"} }

// Number writes a plain decimal ("-1234567.50") with this language's separators.
func (f *Format) Number(plain string) string {
	neg := strings.HasPrefix(plain, "-")
	whole, frac, hasFrac := strings.Cut(strings.TrimPrefix(plain, "-"), ".")
	if n := len(whole); n > 3 && n-3 >= f.MinGroup {
		var b strings.Builder
		for i, c := range whole {
			if i > 0 && (n-i)%3 == 0 {
				b.WriteString(f.Group)
			}
			b.WriteRune(c)
		}
		whole = b.String()
	}
	out := whole
	if hasFrac {
		out += f.Decimal + frac
	}
	if neg {
		out = f.Minus + out
	}
	return out
}

// Amount writes minor units with the exponent's decimals: 1116 with 2 is "11.16" in English and "11,16" in German.
func (f *Format) Amount(minor int64, exp int) string { return f.Number(money.FormatMinor(minor, exp)) }

// Money writes Amount followed by the currency code: "11.16 USD" in English, "11,16 USD" (a no-break space) in German.
func (f *Format) Money(minor int64, exp int, currency string) string {
	return f.Amount(minor, exp) + f.MoneyGap + currency
}

// Date writes a day in words: "2 November 2026", "2. November 2026", "2 de noviembre de 2026".
func (f *Format) Date(t time.Time) string {
	return fmt.Sprintf(f.DateFmt, t.Day(), f.Months[t.Month()-1], t.Year())
}

// Period writes a billing period id: a calendar month "2026-10" as "October 2026"; anything else as it is.
func (f *Format) Period(id string) string {
	t, err := time.Parse("2006-01", id)
	if err != nil {
		return id
	}
	return fmt.Sprintf(f.MonthYearFmt, f.Months[t.Month()-1], t.Year())
}

// The digits are Latin in every language, Arabic included: an invoice number, an IBAN and a bank reference are Latin whatever the language, and a page that mixed both
// kinds of digits would be harder to read than one that does not. CLDR's own default for plain "ar" is Arabic-Indic digits; ar-AE, ar-MA and others use Latin. Changing it
// is a table change here (UNVERIFIED with a reader of Arabic; docs/plans/04-hosted-pages-and-portal.md).
var formats = map[string]*Format{
	"en": {Code: "en", Decimal: ".", Group: ",", MinGroup: 1, Minus: "-", MoneyGap: " ", DateFmt: "%d %s %d", MonthYearFmt: "%s %d",
		Months: [12]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}},
	"tr": {Code: "tr", Decimal: ",", Group: ".", MinGroup: 1, Minus: "-", MoneyGap: " ", DateFmt: "%d %s %d", MonthYearFmt: "%s %d",
		Months: [12]string{"Ocak", "Şubat", "Mart", "Nisan", "Mayıs", "Haziran", "Temmuz", "Ağustos", "Eylül", "Ekim", "Kasım", "Aralık"}},
	"de": {Code: "de", Decimal: ",", Group: ".", MinGroup: 1, Minus: "-", MoneyGap: " ", DateFmt: "%d. %s %d", MonthYearFmt: "%s %d",
		Months: [12]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"}},
	"fr": {Code: "fr", Decimal: ",", Group: " ", MinGroup: 1, Minus: "-", MoneyGap: " ", DateFmt: "%d %s %d", MonthYearFmt: "%s %d",
		Months: [12]string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}},
	"es": {Code: "es", Decimal: ",", Group: ".", MinGroup: 2, Minus: "-", MoneyGap: " ", DateFmt: "%d de %s de %d", MonthYearFmt: "%s de %d",
		Months: [12]string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}},
	"ar": {Code: "ar", Decimal: ".", Group: ",", MinGroup: 1, Minus: "\u200e-", MoneyGap: " ", DateFmt: "%d %s %d", MonthYearFmt: "%s %d",
		Months: [12]string{"يناير", "فبراير", "مارس", "أبريل", "مايو", "يونيو", "يوليو", "أغسطس", "سبتمبر", "أكتوبر", "نوفمبر", "ديسمبر"}},
}
