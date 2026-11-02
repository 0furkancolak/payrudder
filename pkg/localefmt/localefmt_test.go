package localefmt

import (
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestNumbersMoneyAndDatesGolden(t *testing.T) {
	day := time.Date(2026, 11, 2, 23, 59, 0, 0, time.UTC)
	for _, c := range []struct {
		loc                                                         string
		small, big, neg, zero, jpy, quantity, fiveDigits, date, per string
	}{
		{"en", "11.16 USD", "1,234,567.89 USD", "-50.00 USD", "0.00 USD", "1,016 JPY", "1,250.500", "12,345.00 USD", "2 November 2026", "October 2026"},
		{"tr", "11,16 USD", "1.234.567,89 USD", "-50,00 USD", "0,00 USD", "1.016 JPY", "1.250,500", "12.345,00 USD", "2 Kasım 2026", "Ekim 2026"},
		{"de", "11,16 USD", "1.234.567,89 USD", "-50,00 USD", "0,00 USD", "1.016 JPY", "1.250,500", "12.345,00 USD", "2. November 2026", "Oktober 2026"},
		{"fr", "11,16 USD", "1 234 567,89 USD", "-50,00 USD", "0,00 USD", "1 016 JPY", "1 250,500", "12 345,00 USD", "2 novembre 2026", "octobre 2026"},
		// Spanish groups from five digits: 1234,56 but 12.345,67.
		{"es", "11,16 USD", "1.234.567,89 USD", "-50,00 USD", "0,00 USD", "1016 JPY", "1250,500", "12.345,00 USD", "2 de noviembre de 2026", "octubre de 2026"},
		// Arabic: Latin digits (see formats), a left-to-right mark in front of the minus so that it stays on the left of the digits in right-to-left text.
		{"ar", "11.16 USD", "1,234,567.89 USD", "\u200e-50.00 USD", "0.00 USD", "1,016 JPY", "1,250.500", "12,345.00 USD", "2 نوفمبر 2026", "أكتوبر 2026"},
	} {
		f := For(c.loc)
		got := []string{f.Money(1116, 2, "USD"), f.Money(123456789, 2, "USD"), f.Money(-5000, 2, "USD"), f.Money(0, 2, "USD"), f.Money(1016, 0, "JPY"), f.Number("1250.500"),
			f.Money(1234500, 2, "USD"), f.Date(day), f.Period("2026-10")}
		want := []string{c.small, c.big, c.neg, c.zero, c.jpy, c.quantity, c.fiveDigits, c.date, c.per}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s #%d: %q, want %q", c.loc, i, got[i], want[i])
			}
		}
	}
	// A period that is not a calendar month is printed as it is.
	if got := For("en").Period("2026-W44"); got != "2026-W44" {
		t.Errorf("period = %q", got)
	}
	// Amount is the number alone, for a page that prints the currency next to it its own way.
	if got := For("de").Amount(-123456, 2); got != "-1.234,56" {
		t.Errorf("Amount = %q", got)
	}
}

// An unknown language reads as English; every language has all twelve months and a date that names each of the three parts.
func TestEveryLanguageIsComplete(t *testing.T) {
	if For("xx") != For("en") || For("") != For("en") {
		t.Error("an unknown language is English")
	}
	for _, code := range Languages() {
		f := For(code)
		if f.Code != code {
			t.Errorf("%s calls itself %q", code, f.Code)
		}
		seen := map[string]bool{}
		for i, m := range f.Months {
			if strings.TrimSpace(m) == "" || seen[m] {
				t.Errorf("%s: month %d is %q", code, i+1, m)
			}
			seen[m] = true
		}
		if strings.Count(f.DateFmt, "%") != 3 || strings.Count(f.MonthYearFmt, "%") != 2 || f.Decimal == f.Group || f.Minus == "" {
			t.Errorf("%s: separators or formats are wrong: %+v", code, f)
		}
		// The first of January is the first month, the 31st of December the last.
		if got := f.Date(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); !strings.Contains(got, f.Months[0]) {
			t.Errorf("%s: %q", code, got)
		}
		if got := f.Date(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)); !strings.Contains(got, f.Months[11]) || !strings.Contains(got, "31") {
			t.Errorf("%s: %q", code, got)
		}
	}
	if len(formats) != len(Languages()) {
		t.Errorf("%d formats for %d languages", len(formats), len(Languages()))
	}
	// Digits are Latin in every language (see formats): no Arabic-Indic digit slips in through a month, a separator or a gap.
	for _, code := range Languages() {
		f := For(code)
		for _, s := range append(f.Months[:], f.Decimal, f.Group, f.Minus, f.MoneyGap, f.DateFmt, f.MonthYearFmt) {
			for _, r := range s {
				if unicode.IsDigit(r) {
					t.Errorf("%s: a digit %q in a table entry", code, r)
				}
			}
		}
	}
}
