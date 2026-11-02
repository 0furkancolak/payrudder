package blocknotice

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/mailcategory"
	"github.com/0furkancolak/payrudder/services/notifier/internal/templates"
)

func TestMailData(t *testing.T) {
	until := time.Date(2026, 11, 6, 21, 30, 0, 0, time.UTC)
	blocked := &eventsv1.CustomerBlockNotice{Kind: "blocked", ServiceSuspended: true, BlockedUntil: timestamppb.New(until)}

	d := MailData(blocked, "Ada", "Acme", "Europe/Istanbul", "")
	// 21:30 UTC is already the next day in Istanbul: the customer reads the seller's calendar.
	if d["customer_name"] != "Ada" || d["tenant_name"] != "Acme" || d["service_suspended"] != "true" || d["until"] != "7 November 2026, 00:30 (Europe/Istanbul)" {
		t.Fatalf("data = %v", d)
	}
	if got := MailData(blocked, "Ada", "Acme", "Not/AZone", "tr")["until"]; got != "6 Kasım 2026, 21:30 (UTC)" {
		t.Errorf("an unknown zone is UTC, the day is written in the mail's language: %q", got)
	}
	plain := MailData(&eventsv1.CustomerBlockNotice{Kind: "blocked"}, "Ada", "Acme", "", "")
	if _, ok := plain["until"]; ok {
		t.Errorf("a block with no end has no 'until': %v", plain)
	}
	if _, ok := plain["service_suspended"]; ok {
		t.Errorf("a block that does not suspend the service has no 'service_suspended': %v", plain)
	}
	lifted := MailData(&eventsv1.CustomerBlockNotice{Kind: "unblocked", ServiceSuspended: true, BlockedUntil: timestamppb.New(until)}, "Ada", "Acme", "", "")
	if len(lifted) != 2 {
		t.Errorf("a lift says only who and from whom: %v", lifted)
	}
}

// Both notices exist in every language, say what happened, never carry the word of a reason, and are in the category that is off until a seller switches it on.
func TestTheNoticesInEveryLanguage(t *testing.T) {
	until := timestamppb.New(time.Date(2026, 11, 6, 21, 30, 0, 0, time.UTC))
	for _, locale := range templates.Locales {
		blocked := MailData(&eventsv1.CustomerBlockNotice{Kind: "blocked", ServiceSuspended: true, BlockedUntil: until}, "Ada", "Acme", "UTC", locale)
		blocked["locale"] = locale
		suspended, err := templates.Render(BlockedTemplate, blocked, false)
		if err != nil || !strings.Contains(suspended.Text, "Ada") || !strings.Contains(suspended.Text, "Acme") || !strings.Contains(suspended.Text, blocked["until"]) {
			t.Fatalf("%s blocked: %v %q", locale, err, suspended.Text)
		}
		plain := MailData(&eventsv1.CustomerBlockNotice{Kind: "blocked"}, "Ada", "Acme", "UTC", locale)
		plain["locale"] = locale
		open, err := templates.Render(BlockedTemplate, plain, false)
		if err != nil || strings.Contains(open.Text, "{{") || len(open.Text) >= len(suspended.Text) {
			t.Errorf("%s: a block with no end and no suspension is the shorter mail (%d, %d): %v", locale, len(open.Text), len(suspended.Text), err)
		}
		lift, err := templates.Render(UnblockedTemplate, map[string]string{"customer_name": "Ada", "tenant_name": "Acme", "locale": locale}, false)
		if err != nil || !strings.Contains(lift.Text, "Acme") || lift.Subject == suspended.Subject {
			t.Errorf("%s unblocked: %v %q", locale, err, lift.Text)
		}
		if locale != "en" && (suspended.Subject == "Your account with Acme has been restricted" || lift.Subject == "Your account with Acme is no longer restricted") {
			t.Errorf("%s: the subject is not translated: %q, %q", locale, suspended.Subject, lift.Subject)
		}
	}
	for _, name := range []string{BlockedTemplate, UnblockedTemplate} {
		if got := mailcategory.ForTemplate(name); got != mailcategory.CustomerNotices {
			t.Errorf("%s is in the category %q", name, got)
		}
	}
	// The seller's words cannot get into the mail: the template reads no key for them, and the customer's own name is escaped.
	d := map[string]string{"customer_name": "<b>Ada</b>", "tenant_name": "Acme", "reason": "stolen card", "category": "fraud"}
	r, err := templates.Render(BlockedTemplate, d, false)
	if err != nil || strings.Contains(r.HTML, "stolen card") || strings.Contains(r.HTML, "fraud") || strings.Contains(r.HTML, "<b>Ada</b>") {
		t.Errorf("the mail shows the seller's reason, or an unescaped name: %v %s", err, r.HTML)
	}
}

func TestTemplateOf(t *testing.T) {
	for kind, want := range map[string]string{"blocked": BlockedTemplate, "unblocked": UnblockedTemplate} {
		if got, ok := templateOf(kind); !ok || got != want {
			t.Errorf("templateOf(%q) = %q, %v", kind, got, ok)
		}
	}
	if _, ok := templateOf("suspended"); ok {
		t.Error("an unknown kind can never be mailed")
	}
}
