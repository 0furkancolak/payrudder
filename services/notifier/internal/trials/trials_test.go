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

	d := mailData(ev, "Ada", "Acme", "Europe/Istanbul")
	if d["customer_name"] != "Ada" || d["tenant_name"] != "Acme" || d["plan_code"] != "pro" {
		t.Fatalf("data = %v", d)
	}
	// 21:30 UTC is already the next day in Istanbul (UTC+3): the customer reads the tenant's calendar.
	if d["trial_end_date"] != "7 Nov 2026" || d["trial_end"] != "7 Nov 2026, 00:30 (Europe/Istanbul)" {
		t.Errorf("trial_end = %q, %q", d["trial_end_date"], d["trial_end"])
	}
	for _, zone := range []string{"", "Not/AZone"} {
		if got := mailData(ev, "Ada", "Acme", zone)["trial_end"]; got != "6 Nov 2026, 21:30 (UTC)" {
			t.Errorf("zone %q: trial_end = %q", zone, got)
		}
	}

	r, err := templates.Render(Template, d, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Subject != "Your pro trial ends on 7 Nov 2026" {
		t.Errorf("subject = %q", r.Subject)
	}
	if !strings.Contains(r.Text, "7 Nov 2026, 00:30 (Europe/Istanbul)") || !strings.Contains(r.Text, "Hi Ada") || strings.Contains(r.Text, "&") {
		t.Errorf("text = %q", r.Text)
	}
}
