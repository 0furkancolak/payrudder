package renewalmail

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/services/notifier/internal/templates"
)

func TestMailData(t *testing.T) {
	renews := time.Date(2026, 12, 5, 22, 30, 0, 0, time.UTC)
	ev := &eventsv1.RenewalNotice{PlanCode: "basic", RenewsAt: timestamppb.New(renews), PriceMinor: 123456, Currency: "USD", CurrencyExponent: 2, Interval: "year", CancelText: "Cancel in the panel.\nOr write to us."}
	d := MailData(ev, "Ada", "Acme", "Europe/Istanbul", "")
	// 22:30 UTC is already the next day in Istanbul (UTC+3): the customer reads the tenant's calendar.
	if d["renewal_date"] != "6 December 2026" || d["price"] != "1,234.56 USD" || d["plan_code"] != "basic" || d["interval"] != "year" || d["cancel_text"] != ev.GetCancelText() {
		t.Errorf("data = %v", d)
	}
	for _, zone := range []string{"", "Not/AZone"} {
		if got := MailData(ev, "Ada", "Acme", zone, "")["renewal_date"]; got != "5 December 2026" {
			t.Errorf("zone %q: renewal_date = %q", zone, got)
		}
	}
	// A currency without minor units is not divided.
	if got := MailData(&eventsv1.RenewalNotice{RenewsAt: timestamppb.New(renews), PriceMinor: 5000, Currency: "JPY"}, "", "", "", "")["price"]; got != "5,000 JPY" {
		t.Errorf("JPY price = %q", got)
	}
}

// Every language says the price, the date and the seller's words; a seller who wrote none gets the plain "tell us before" sentence instead of an empty heading.
func TestEveryLanguageRendersTheNotice(t *testing.T) {
	ev := &eventsv1.RenewalNotice{PlanCode: "basic", RenewsAt: timestamppb.New(time.Date(2026, 12, 5, 12, 0, 0, 0, time.UTC)), PriceMinor: 4000, Currency: "EUR", CurrencyExponent: 2, Interval: "month", CancelText: "Cancel <b>here</b>."}
	for _, loc := range []string{"", "en", "tr", "de", "fr", "es", "ar"} {
		d := MailData(ev, "Ada", "Acme", "", "")
		d["locale"] = loc
		r, err := templates.Render(Template, d, false)
		if err != nil {
			t.Fatalf("%q: %v", loc, err)
		}
		for _, want := range []string{"40.00 EUR", "5 December 2026", "Cancel &lt;b&gt;here&lt;/b&gt;.", "Acme", "Ada"} {
			if !strings.Contains(r.HTML, want) {
				t.Errorf("%q: the mail lacks %q: %s", loc, want, r.HTML)
			}
		}
		if strings.Contains(r.HTML, "<b>here</b>") {
			t.Errorf("%q: the seller's text is printed escaped", loc)
		}
		if r.Subject == "" || !strings.Contains(r.Subject, "5 December 2026") {
			t.Errorf("%q: subject %q", loc, r.Subject)
		}
		d["cancel_text"] = ""
		plain, err := templates.Render(Template, d, false)
		if err != nil || strings.Contains(plain.HTML, "<h3>") {
			t.Errorf("%q: with no words of the seller there is no empty heading: %v", loc, err)
		}
	}
	year := MailData(&eventsv1.RenewalNotice{RenewsAt: timestamppb.Now(), PriceMinor: 100, Currency: "USD", CurrencyExponent: 2, Interval: "year"}, "A", "B", "", "")
	month := MailData(&eventsv1.RenewalNotice{RenewsAt: timestamppb.Now(), PriceMinor: 100, Currency: "USD", CurrencyExponent: 2, Interval: "month"}, "A", "B", "", "")
	y, _ := templates.Render(Template, year, false)
	m, _ := templates.Render(Template, month, false)
	if !strings.Contains(y.HTML, "per year") || !strings.Contains(m.HTML, "per month") {
		t.Errorf("the interval is said: %q / %q", y.Text, m.Text)
	}
}

// The date and the price are written the way the language of the mail writes them (pkg/localefmt).
func TestMailDataWritesTheDateAndThePriceInTheMailsLanguage(t *testing.T) {
	ev := &eventsv1.RenewalNotice{PlanCode: "basic", RenewsAt: timestamppb.New(time.Date(2026, 12, 5, 22, 30, 0, 0, time.UTC)), PriceMinor: 123456, CurrencyExponent: 2, Currency: "USD", Interval: "year"}
	for locale, want := range map[string][2]string{"": {"6 December 2026", "1,234.56 USD"}, "tr": {"6 Aralık 2026", "1.234,56\u00a0USD"}, "de": {"6. Dezember 2026", "1.234,56\u00a0USD"}} {
		d := MailData(ev, "Ada", "Acme", "Europe/Istanbul", locale)
		if d["renewal_date"] != want[0] || d["price"] != want[1] {
			t.Errorf("%q: renewal_date %q, price %q; want %q", locale, d["renewal_date"], d["price"], want)
		}
	}
}
