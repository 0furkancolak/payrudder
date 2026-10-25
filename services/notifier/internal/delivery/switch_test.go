package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/mailcategory"
)

// fakeSettings answers GetMailSettings with the categories in off switched off, and counts the calls.
type fakeSettings struct {
	off   []string
	err   error
	calls int
}

func (f *fakeSettings) GetMailSettings(_ context.Context, _ *plansv1.GetMailSettingsRequest, _ ...grpc.CallOption) (*plansv1.GetMailSettingsResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	resp := &plansv1.GetMailSettingsResponse{}
	for _, c := range mailcategory.All() {
		on := true
		for _, o := range f.off {
			on = on && o != c
		}
		resp.Categories = append(resp.Categories, &plansv1.MailCategorySetting{Category: c, Enabled: on})
	}
	return resp, nil
}

func TestPlansSwitchCachesPerTenant(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := &fakeSettings{off: []string{mailcategory.UsageAlerts}}
	s := &PlansSwitch{Client: f, TTL: 10 * time.Second, Now: func() time.Time { return now }}
	a, b := uuid.New(), uuid.New()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if on, err := s.Enabled(ctx, a, mailcategory.UsageAlerts); err != nil || on {
			t.Fatalf("usage alerts: on=%v err=%v, want off", on, err)
		}
		if on, err := s.Enabled(ctx, a, mailcategory.PaymentReceipts); err != nil || !on {
			t.Fatalf("receipts: on=%v err=%v, want on", on, err)
		}
	}
	if f.calls != 1 {
		t.Fatalf("one tenant looked up %d times, want 1", f.calls)
	}
	if _, err := s.Enabled(ctx, b, mailcategory.System); err != nil || f.calls != 2 {
		t.Fatalf("a second tenant needs its own lookup: calls=%d err=%v", f.calls, err)
	}

	f.off = nil
	now = now.Add(11 * time.Second)
	if on, err := s.Enabled(ctx, a, mailcategory.UsageAlerts); err != nil || !on {
		t.Fatalf("after the TTL a changed switch must be seen: on=%v err=%v", on, err)
	}
}

func TestPlansSwitchLookupFailureIsAnError(t *testing.T) {
	s := &PlansSwitch{Client: &fakeSettings{err: errors.New("plans-svc down")}, TTL: time.Second}
	if on, err := s.Enabled(context.Background(), uuid.New(), mailcategory.System); err == nil || on {
		t.Fatalf("on=%v err=%v: an unreadable setting must fail (the mail is retried), not send", on, err)
	}
}

type stubSwitch struct {
	off map[string]bool
	err error
}

func (s stubSwitch) Enabled(_ context.Context, _ uuid.UUID, category string) (bool, error) {
	return !s.off[category], s.err
}

func TestPermitted(t *testing.T) {
	tid := uuid.New()
	m := &Mailer{Switch: stubSwitch{off: map[string]bool{mailcategory.PaymentFailed: true}}}
	for template, want := range map[string]bool{"invoice_paid.v1": true, "payment_failed.v1": false, "limit_exceeded.v1": true, "other.v1": true} {
		if got, err := m.Permitted(context.Background(), tid, template); err != nil || got != want {
			t.Errorf("Permitted(%s) = %v, %v; want %v", template, got, err, want)
		}
	}
	if ok, _ := (&Mailer{}).Permitted(context.Background(), tid, "payment_failed.v1"); !ok {
		t.Error("without a Switch every mail is permitted")
	}
	if ok, _ := m.Permitted(context.Background(), uuid.Nil, "payment_failed.v1"); !ok {
		t.Error("a mail without a tenant id is permitted")
	}
	failing := &Mailer{Switch: stubSwitch{err: errors.New("down")}}
	if ok, err := failing.Permitted(context.Background(), tid, "invoice_paid.v1"); err == nil || ok {
		t.Errorf("a failed lookup must be an error, got ok=%v err=%v", ok, err)
	}
}
