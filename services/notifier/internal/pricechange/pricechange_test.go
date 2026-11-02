package pricechange

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/notifier/internal/mailevents"
	"github.com/0furkancolak/payrudder/services/notifier/internal/templates"
)

func TestMailData(t *testing.T) {
	start := time.Date(2026, 12, 5, 22, 30, 0, 0, time.UTC)
	ev := &eventsv1.PriceChangeScheduled{PlanCode: "basic", EffectiveAt: timestamppb.New(start), NewRenewalMinor: 123456, Currency: "USD"}

	d := mailData(ev, "Ada", "Acme", "Europe/Istanbul", "")
	if d["customer_name"] != "Ada" || d["tenant_name"] != "Acme" || d["plan_code"] != "basic" {
		t.Fatalf("data = %v", d)
	}
	// 22:30 UTC is already the next day in Istanbul (UTC+3): the customer reads the tenant's calendar.
	if d["effective_date"] != "6 December 2026" || d["new_renewal"] != "1,234.56 USD" {
		t.Errorf("effective_date = %q, new_renewal = %q", d["effective_date"], d["new_renewal"])
	}
	for _, zone := range []string{"", "Not/AZone"} {
		if got := mailData(ev, "Ada", "Acme", zone, "")["effective_date"]; got != "5 December 2026" {
			t.Errorf("zone %q: effective_date = %q", zone, got)
		}
	}
	// A currency without minor units is not divided: 5000 JPY is 5,000 JPY.
	if got := mailData(&eventsv1.PriceChangeScheduled{EffectiveAt: timestamppb.New(start), NewRenewalMinor: 5000, Currency: "JPY"}, "", "", "", "")["new_renewal"]; got != "5,000 JPY" {
		t.Errorf("JPY renewal = %q", got)
	}
}

func TestEveryLanguageRenders(t *testing.T) {
	d := mailData(&eventsv1.PriceChangeScheduled{PlanCode: "basic", EffectiveAt: timestamppb.New(time.Date(2026, 12, 5, 12, 0, 0, 0, time.UTC)), NewRenewalMinor: 4000, Currency: "EUR"}, "Ada", "Acme", "", "")
	for _, loc := range []string{"", "en", "tr", "de", "fr", "es", "ar"} {
		r, err := templates.Render(Template, withLocale(d, loc), false)
		if err != nil {
			t.Fatalf("%q: %v", loc, err)
		}
		if !strings.Contains(r.Subject, "basic") || !strings.Contains(r.Subject, "5 December 2026") || !strings.Contains(r.Text, "40.00 EUR") {
			t.Errorf("%q: subject %q, text %q", loc, r.Subject, r.Text)
		}
	}
}

// The date and the amount are written the way the language of the mail writes them (pkg/localefmt).
func TestMailDataWritesTheDateAndTheAmountInTheMailsLanguage(t *testing.T) {
	ev := &eventsv1.PriceChangeScheduled{PlanCode: "basic", EffectiveAt: timestamppb.New(time.Date(2026, 12, 5, 22, 30, 0, 0, time.UTC)), NewRenewalMinor: 123456, Currency: "USD"}
	for locale, want := range map[string][2]string{"": {"6 December 2026", "1,234.56 USD"}, "tr": {"6 Aralık 2026", "1.234,56\u00a0USD"}, "de": {"6. Dezember 2026", "1.234,56\u00a0USD"},
		"fr": {"6 décembre 2026", "1\u202f234,56\u00a0USD"}, "es": {"6 de diciembre de 2026", "1234,56\u00a0USD"}} {
		d := mailData(ev, "Ada", "Acme", "Europe/Istanbul", locale)
		if d["effective_date"] != want[0] || d["new_renewal"] != want[1] {
			t.Errorf("%q: effective_date %q, new_renewal %q; want %q", locale, d["effective_date"], d["new_renewal"], want)
		}
	}
}

// fakePause answers the kill switch: paused for the first `pausedFor` looks, then not, or never, or with an error.
type fakePause struct {
	Admin
	looks     atomic.Int32
	pausedFor int32
	err       error
}

func (f *fakePause) GetPriceChangePause(context.Context, *plansv1.GetPriceChangePauseRequest, ...grpc.CallOption) (*plansv1.GetPriceChangePauseResponse, error) {
	n := f.looks.Add(1)
	return &plansv1.GetPriceChangePauseResponse{Pause: &plansv1.PriceChangePause{Paused: n <= f.pausedFor}}, f.err
}

// While the operator's kill switch is on no notice goes out: the consumer waits and goes on once it is released, hands the batch back when the wait is long, and
// does not guess when it cannot tell. A batch asks once, not once per notice.
func TestPausedPriceChangesHoldTheMail(t *testing.T) {
	ctx := context.Background()
	t.Run("released while waiting", func(t *testing.T) {
		f := &fakePause{pausedFor: 2}
		c := &Consumer{Admin: f, PauseLook: 5 * time.Millisecond, HeldFor: time.Second}
		if err := c.holdWhilePaused(ctx); err != nil || f.looks.Load() != 3 {
			t.Fatalf("err %v after %d looks, want nil after 3", err, f.looks.Load())
		}
	})
	t.Run("still paused: the batch is handed back", func(t *testing.T) {
		c := &Consumer{Admin: &fakePause{pausedFor: 1 << 20}, PauseLook: 5 * time.Millisecond, HeldFor: 40 * time.Millisecond}
		if err := c.holdWhilePaused(ctx); !errors.Is(err, errHeld) {
			t.Fatalf("err = %v, want errHeld", err)
		}
	})
	t.Run("an answer is kept for a look, so a thousand notices ask once", func(t *testing.T) {
		f := &fakePause{}
		c := &Consumer{Admin: f, PauseLook: time.Minute}
		for range 1000 {
			if err := c.holdWhilePaused(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if f.looks.Load() != 1 {
			t.Fatalf("looks = %d, want 1", f.looks.Load())
		}
	})
	t.Run("no answer is no guess", func(t *testing.T) {
		c := &Consumer{Admin: &fakePause{err: errors.New("plans-svc is down")}}
		if err := c.holdWhilePaused(ctx); err == nil || !strings.Contains(err.Error(), "plans-svc is down") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a cancelled consumer stops waiting", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		c := &Consumer{Admin: &fakePause{pausedFor: 1 << 20}, PauseLook: time.Hour, HeldFor: 2 * time.Hour}
		if err := c.holdWhilePaused(cctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
}

// The console's preview is the mail the consumer would send: the same data, in the customer's language, the date in the tenant's zone and the amount written the
// way the language writes it.
func TestPreviewIsTheMailTheConsumerWouldSend(t *testing.T) {
	in := mailevents.PriceChangePreview{Locale: "de", CustomerName: "Ada", TenantName: "Acme", PlanCode: "pro", EffectiveTime: time.Date(2026, 12, 5, 23, 30, 0, 0, time.UTC),
		NewRenewalMinor: 123456, Currency: "EUR", Timezone: "Europe/Berlin"}
	r, err := Preview(in)
	if err != nil {
		t.Fatal(err)
	}
	want, err := templates.Render(Template, withLocale(mailData(&eventsv1.PriceChangeScheduled{PlanCode: "pro", EffectiveAt: timestamppb.New(in.EffectiveTime), NewRenewalMinor: 123456, Currency: "EUR"}, "Ada", "Acme", "Europe/Berlin", "de"), "de"), false)
	if err != nil || r.Subject != want.Subject || r.HTML != want.HTML || r.Text != want.Text {
		t.Fatalf("preview differs from the mail: %v\n%q\n%q", err, r.Subject, want.Subject)
	}
	// 23:30 UTC is the next day in Berlin, and a German reader gets a German date and amount.
	if !strings.Contains(r.Subject, "pro") || !strings.Contains(r.Text, "1.234,56") || !strings.Contains(r.Subject, "6. Dezember 2026") {
		t.Errorf("subject %q, text %q", r.Subject, r.Text)
	}
}
