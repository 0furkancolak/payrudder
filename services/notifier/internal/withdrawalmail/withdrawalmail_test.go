package withdrawalmail

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/services/notifier/internal/templates"
)

func TestMailDataAndEveryLanguage(t *testing.T) {
	at := time.Date(2026, 11, 2, 7, 30, 0, 0, time.UTC)
	ev := &eventsv1.ContractWithdrawalReceived{Statement: "Name: Ada\nContract: invoice INV-7\nI withdraw <now>.", ReceivedAt: timestamppb.New(at)}
	d := MailData(ev, "Ada", "Acme", "Europe/Istanbul", "")
	if !strings.HasPrefix(d["received_at"], "2 November 2026, 10:30") || d["statement"] != ev.GetStatement() {
		t.Fatalf("data = %v: the time of day is the tenant's, the statement is verbatim", d)
	}
	if utc := MailData(ev, "Ada", "Acme", "Not/AZone", ""); !strings.Contains(utc["received_at"], "UTC") {
		t.Errorf("an unknown zone is UTC: %v", utc)
	}
	for _, loc := range []string{"", "en", "tr", "de", "fr", "es", "ar"} {
		d["locale"] = loc
		r, err := templates.Render(Template, d, false)
		if err != nil {
			t.Fatalf("%q: %v", loc, err)
		}
		// The acknowledgement carries what was submitted and when (Directive 2011/83/EU art. 11a(3)): both, and the statement escaped.
		for _, want := range []string{"2 November 2026, 10:30", "Contract: invoice INV-7", "I withdraw &lt;now&gt;.", "Acme", "Ada"} {
			if !strings.Contains(r.HTML, want) {
				t.Errorf("%q: the mail lacks %q: %s", loc, want, r.HTML)
			}
		}
		if r.Subject == "" {
			t.Errorf("%q: no subject", loc)
		}
	}
}

// The day is written the way the language of the mail writes it (pkg/localefmt), the time of day and the zone as before.
func TestMailDataWritesTheDayInTheMailsLanguage(t *testing.T) {
	ev := &eventsv1.ContractWithdrawalReceived{Statement: "x", ReceivedAt: timestamppb.New(time.Date(2026, 11, 2, 7, 30, 0, 0, time.UTC))}
	for locale, want := range map[string]string{"": "2 November 2026, 10:30", "tr": "2 Kasım 2026, 10:30", "de": "2. November 2026, 10:30"} {
		if got := MailData(ev, "Ada", "Acme", "Europe/Istanbul", locale)["received_at"]; !strings.HasPrefix(got, want) {
			t.Errorf("%q: received_at %q; want it to start with %q", locale, got, want)
		}
	}
}
