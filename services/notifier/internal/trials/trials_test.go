package trials

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/services/notifier/internal/templates"
)

func TestMailData(t *testing.T) {
	end := time.Date(2026, 11, 6, 21, 30, 0, 0, time.UTC)
	ev := &eventsv1.TrialWillEnd{PlanCode: "pro", TrialEndsAt: timestamppb.New(end)}

	d := mailData(ev, "Ada", "Acme", "Europe/Istanbul", "")
	if d["customer_name"] != "Ada" || d["tenant_name"] != "Acme" || d["plan_code"] != "pro" {
		t.Fatalf("data = %v", d)
	}
	// 21:30 UTC is already the next day in Istanbul (UTC+3): the customer reads the tenant's calendar.
	if d["trial_end_date"] != "7 November 2026" || d["trial_end"] != "7 November 2026, 00:30 (Europe/Istanbul)" {
		t.Errorf("trial_end = %q, %q", d["trial_end_date"], d["trial_end"])
	}
	for _, zone := range []string{"", "Not/AZone"} {
		if got := mailData(ev, "Ada", "Acme", zone, "")["trial_end"]; got != "6 November 2026, 21:30 (UTC)" {
			t.Errorf("zone %q: trial_end = %q", zone, got)
		}
	}

	r, err := templates.Render(Template, d, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Subject != "Your pro trial ends on 7 November 2026" {
		t.Errorf("subject = %q", r.Subject)
	}
	if !strings.Contains(r.Text, "7 November 2026, 00:30 (Europe/Istanbul)") || !strings.Contains(r.Text, "Hi Ada") || strings.Contains(r.Text, "&") {
		t.Errorf("text = %q", r.Text)
	}
}

// The date is written the way the language of the mail writes it (pkg/localefmt), not in one English style.
func TestMailDataWritesTheDateInTheMailsLanguage(t *testing.T) {
	ev := &eventsv1.TrialWillEnd{PlanCode: "pro", TrialEndsAt: timestamppb.New(time.Date(2026, 11, 6, 21, 30, 0, 0, time.UTC))}
	for locale, want := range map[string]string{"": "7 November 2026", "en": "7 November 2026", "tr": "7 Kasım 2026", "de": "7. November 2026", "fr": "7 novembre 2026", "es": "7 de noviembre de 2026", "ar": "7 نوفمبر 2026"} {
		d := mailData(ev, "Ada", "Acme", "Europe/Istanbul", locale)
		if d["trial_end_date"] != want || d["trial_end"] != want+", 00:30 (Europe/Istanbul)" {
			t.Errorf("%q: trial_end_date %q, trial_end %q; want %q", locale, d["trial_end_date"], d["trial_end"], want)
		}
	}
}
