package httpapi

import (
	"strings"
	"testing"
)

// The portal shows the customer's subscriptions, read-only, in the customer's language and number and date conventions.
func TestPortalShowsTheSubscriptionSummary(t *testing.T) {
	ts := withWorker(t, &fakeWorker{}, false)
	_, en := get(t, ts, "/c/"+portalTok)
	for _, want := range []string{">Subscription<", "Pro &lt;i&gt;plan&lt;/i&gt;", ">Trial<", "87.00&nbsp;USD / month", "Quantity: 3", "Trial ends on 1 December 2026",
		"Team", ">Active<", "12,345.00&nbsp;EUR / year", "Ends on 30 November 2026", "Billed and renewed by the payment provider.", ">Payment overdue<", "Changes to Basic on 1 January 2027",
		"To change your plan or cancel, contact the seller."} {
		if !strings.Contains(strings.ReplaceAll(en, " ", "&nbsp;"), want) {
			t.Errorf("the English portal lacks %q", want)
		}
	}
	if strings.Contains(en, "<i>plan</i>") {
		t.Error("a plan name was printed unescaped")
	}
	// The summary offers no action: it is the seller's to change a plan or cancel (owner decision, plan 04).
	rest := en[strings.Index(en, ">Subscription<"):]
	section := rest[:1+strings.Index(rest[1:], `<h2 class="sec">`)]
	for _, bad := range []string{"<form", "<button", "cancel_at_period_end"} {
		if strings.Contains(section, bad) {
			t.Errorf("the subscription summary has %q: customers cannot change a subscription from the portal", bad)
		}
	}

	_, tr := get(t, ts, "/c/"+portalTok+"?lang=tr")
	for _, want := range []string{">Abonelik<", "87,00&nbsp;USD / ay", "Adet: 3", "Deneme süresi 1 Aralık 2026 tarihinde bitiyor", "30 Kasım 2026 tarihinde sona erecek", "1 Ocak 2027 tarihinde Basic planına geçilecek", "12.345,00&nbsp;EUR / yıl"} {
		if !strings.Contains(strings.ReplaceAll(tr, " ", "&nbsp;"), want) {
			t.Errorf("the Turkish portal lacks %q", want)
		}
	}
	_, ar := get(t, ts, "/c/"+portalTok+"?lang=ar")
	for _, want := range []string{">الاشتراك<", "ينتهي في 30 نوفمبر 2026", "التغيير إلى Basic في 1 يناير 2027"} {
		if !strings.Contains(ar, want) {
			t.Errorf("the Arabic portal lacks %q", want)
		}
	}
}

// plans-svc being down takes the section away, not the page.
func TestPortalWithoutTheSubscriptionSummaryStillShows(t *testing.T) {
	portalSubsDown.Store(true)
	t.Cleanup(func() { portalSubsDown.Store(false) })
	ts := withWorker(t, &fakeWorker{}, false)
	resp, page := get(t, ts, "/c/"+portalTok)
	if resp.StatusCode != 200 || strings.Contains(page, ">Subscription<") || !strings.Contains(page, "INV-2026-000009") {
		t.Errorf("portal without subscriptions: %d\n%s", resp.StatusCode, page)
	}
}
