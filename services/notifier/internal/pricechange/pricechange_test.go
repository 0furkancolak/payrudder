package pricechange

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/services/notifier/internal/templates"
)

func TestMailData(t *testing.T) {
	start := time.Date(2026, 12, 5, 22, 30, 0, 0, time.UTC)
	ev := &eventsv1.PriceChangeScheduled{PlanCode: "basic", EffectiveAt: timestamppb.New(start), NewRenewalMinor: 123456, Currency: "USD"}

	d := mailData(ev, "Ada", "Acme", "Europe/Istanbul")
	if d["customer_name"] != "Ada" || d["tenant_name"] != "Acme" || d["plan_code"] != "basic" {
		t.Fatalf("data = %v", d)
	}
	// 22:30 UTC is already the next day in Istanbul (UTC+3): the customer reads the tenant's calendar.
	if d["effective_date"] != "6 Dec 2026" || d["new_renewal"] != "1,234.56 USD" {
		t.Errorf("effective_date = %q, new_renewal = %q", d["effective_date"], d["new_renewal"])
	}
	for _, zone := range []string{"", "Not/AZone"} {
		if got := mailData(ev, "Ada", "Acme", zone)["effective_date"]; got != "5 Dec 2026" {
			t.Errorf("zone %q: effective_date = %q", zone, got)
		}
	}
	// A currency without minor units is not divided: 5000 JPY is 5,000 JPY.
	if got := mailData(&eventsv1.PriceChangeScheduled{EffectiveAt: timestamppb.New(start), NewRenewalMinor: 5000, Currency: "JPY"}, "", "", "")["new_renewal"]; got != "5,000 JPY" {
		t.Errorf("JPY renewal = %q", got)
	}
}

func TestEveryLanguageRenders(t *testing.T) {
	d := mailData(&eventsv1.PriceChangeScheduled{PlanCode: "basic", EffectiveAt: timestamppb.New(time.Date(2026, 12, 5, 12, 0, 0, 0, time.UTC)), NewRenewalMinor: 4000, Currency: "EUR"}, "Ada", "Acme", "")
	for _, loc := range []string{"", "en", "tr", "de", "fr", "es", "ar"} {
		r, err := templates.Render(Template, withLocale(d, loc), false)
		if err != nil {
			t.Fatalf("%q: %v", loc, err)
		}
		if !strings.Contains(r.Subject, "basic") || !strings.Contains(r.Subject, "5 Dec 2026") || !strings.Contains(r.Text, "40.00 EUR") {
			t.Errorf("%q: subject %q, text %q", loc, r.Subject, r.Text)
		}
	}
}
