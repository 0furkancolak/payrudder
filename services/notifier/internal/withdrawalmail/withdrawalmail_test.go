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

// What the customer typed is mailed back in their own acknowledgement and shown to the seller: whatever it holds, the mail has it as text. This is html/template's doing, and
// this test is what keeps it so (a template that printed the statement as trusted HTML would pass every other test).
func TestWhatTheCustomerTypedIsMailedAsTextInEveryLanguage(t *testing.T) {
	hostile := "Name: <script>alert(1)</script> \"quoted\" 'single' &amp; <img src=x onerror=alert(1)>\nI withdraw: </blockquote><h1>sign here</h1> <a href=\"javascript:alert(2)\">click</a>"
	ev := &eventsv1.ContractWithdrawalReceived{Statement: hostile, ReceivedAt: timestamppb.New(time.Date(2026, 11, 2, 7, 30, 0, 0, time.UTC))}
	d := MailData(ev, "Ada <b>Lovelace</b>", "Acme <i>Ltd</i>", "UTC", "")
	for _, loc := range []string{"", "en", "tr", "de", "fr", "es", "ar"} {
		d["locale"] = loc
		r, err := templates.Render(Template, d, false)
		if err != nil {
			t.Fatalf("%q: %v", loc, err)
		}
		for _, raw := range []string{"<script", "<img", "<h1>", "</blockquote><", "<a href", "<b>Lovelace", "<i>Ltd", `"quoted"`} {
			if strings.Contains(r.HTML, raw) {
				t.Errorf("%q: the mail holds %q as markup:\n%s", loc, raw, r.HTML)
			}
		}
		for _, want := range []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "&#34;quoted&#34;", "&amp;amp;", "&lt;img src=x onerror=alert(1)&gt;", "&lt;/blockquote&gt;&lt;h1&gt;sign here&lt;/h1&gt;"} {
			if !strings.Contains(r.HTML, want) {
				t.Errorf("%q: the mail lacks the escaped %q", loc, want)
			}
		}
		if strings.Contains(r.Subject, "<") {
			t.Errorf("%q: the subject %q", loc, r.Subject)
		}
	}
}
