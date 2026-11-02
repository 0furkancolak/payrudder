package httpapi

import (
	"strings"
	"testing"
)

// Numbers and dates on the public pages follow the language of the page (pkg/localefmt, the same table as the invoice PDF), not one fixed style.
func TestHostedPagesWriteNumbersAndDatesInThePagesLanguage(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, c := range []struct {
		lang string
		want []string
	}{
		{"en", []string{"1,234.56", "-1,234.61", "1 October 2026 – 1 November 2026", ">1 October 2026<"}},
		{"de", []string{"1.234,56", "-1.234,61", "1. Oktober 2026 – 1. November 2026", ">1. Oktober 2026<"}},
		{"fr", []string{"1 234,56", "-1 234,61", "1 octobre 2026 – 1 novembre 2026"}},
		{"es", []string{"1234,56", "-1234,61", "1 de octubre de 2026 – 1 de noviembre de 2026"}},
		{"tr", []string{"1.234,56", "-1.234,61", "1 Ekim 2026 – 1 Kasım 2026"}},
		// Latin digits, a left-to-right mark in front of the minus so that it stays on the left of the digits in right-to-left text, and Arabic month names.
		{"ar", []string{"1,234.56", "\u200e-1,234.61", "1 أكتوبر 2026 – 1 نوفمبر 2026", ">1 أكتوبر 2026<"}},
	} {
		_, page := get(t, ts, "/i/"+goodToken+"?lang="+c.lang)
		for _, want := range c.want {
			if !strings.Contains(page, want) {
				t.Errorf("%s: the invoice lacks %q", c.lang, want)
			}
		}
		if strings.Contains(page, "2026-10-01") {
			t.Errorf("%s: an ISO date is still on the page", c.lang)
		}
	}
}
