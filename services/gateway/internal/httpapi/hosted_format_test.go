package httpapi

import (
	"strings"
	"testing"
	"time"
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

// The days on a page are the days of the seller's calendar (docs/plans/05): an invoice issued at 22:30 UTC on 31 December is the first of January for a seller in Istanbul,
// and its service period runs to the midnight there. An invoice that froze no zone is dated by UTC, as every page was.
func TestHostedPagesWriteDaysInTheSellersTimeZone(t *testing.T) {
	ts, _ := newTestServer(t)
	_, page := get(t, ts, "/i/"+zoneToken)
	for _, want := range []string{">1 January 2027<", "1 December 2026 – 1 January 2027"} {
		if !strings.Contains(page, want) {
			t.Errorf("the invoice of an Istanbul seller lacks %q", want)
		}
	}
	_, page = get(t, ts, "/i/"+noZoneToken)
	for _, want := range []string{">31 December 2026<", "30 November 2026 – 31 December 2026"} {
		if !strings.Contains(page, want) {
			t.Errorf("the invoice without a zone lacks %q", want)
		}
	}
}

// The portal lists its days in the seller's zone too.
func TestPortalWritesDaysInTheSellersTimeZone(t *testing.T) {
	ts, _ := newTestServer(t)
	_, page := get(t, ts, "/c/"+portalZoneTok)
	if !strings.Contains(page, "1 January 2027") || strings.Contains(page, "31 December 2026") {
		t.Errorf("the portal of an Istanbul seller: %s", page)
	}
}

// pageBase.Day follows its zone; a name that does not load is UTC.
func TestPageBaseDayFollowsItsZone(t *testing.T) {
	at := time.Date(2026, 12, 31, 22, 30, 0, 0, time.UTC)
	for zone, want := range map[string]string{"Europe/Istanbul": "1 January 2027", "Pacific/Honolulu": "31 December 2026", "": "31 December 2026", "Not/AZone": "31 December 2026"} {
		b := newBase("en")
		b.Zone = zoneOf(zone)
		if got := b.Day(at); got != want {
			t.Errorf("zone %q: %s, want %s", zone, got, want)
		}
	}
}
