package model

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
)

var (
	now    = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	tenant = uuid.MustParse("0192a000-0000-7000-8000-000000000001")
	cust   = uuid.MustParse("0192a000-0000-7000-8000-000000000002")
)

func TestRemindable(t *testing.T) {
	in := func(d time.Duration) *time.Time { e := now.Add(d); return &e }
	day := 24 * time.Hour
	base := Trial{State: "trialing", OpenEnded: true, Owner: domain.OwnerPayrudder, EndsAt: in(2 * day)}
	with := func(f func(*Trial)) Trial { c := base; f(&c); return c }
	cases := []struct {
		name string
		tr   Trial
		want bool
	}{
		{"ends inside the window", base, true},
		{"ends exactly at the window's edge", with(func(c *Trial) { c.EndsAt = in(3 * day) }), true},
		{"ends after the window", with(func(c *Trial) { c.EndsAt = in(3*day + time.Second) }), false},
		{"ends right now: too late to remind", with(func(c *Trial) { c.EndsAt = in(0) }), false},
		{"ended already", with(func(c *Trial) { c.EndsAt = in(-time.Hour) }), false},
		{"no trial end", with(func(c *Trial) { c.EndsAt = nil }), false},
		{"already active", with(func(c *Trial) { c.State = "active" }), false},
		{"paused", with(func(c *Trial) { c.State = "paused" }), false},
		{"cancelled", with(func(c *Trial) { c.State = "cancelled" }), false},
		{"ended or ending (not open-ended)", with(func(c *Trial) { c.OpenEnded = false }), false},
		{"cancels at the end of the period: it will not convert", with(func(c *Trial) { c.CancelAtPeriodEnd = true }), false},
		{"a provider runs it: the provider reminds", with(func(c *Trial) { c.Owner = domain.OwnerProvider }), false},
	}
	for _, c := range cases {
		if got := c.tr.Remindable(now, Window(3)); got != c.want {
			t.Errorf("%s: Remindable = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWindow(t *testing.T) {
	if got := Window(3); got != 72*time.Hour {
		t.Errorf("Window(3) = %v", got)
	}
	if got := Window(0); got != DefaultDays*24*time.Hour {
		t.Errorf("Window(0) = %v, want the default", got)
	}
	if got := Window(-2); got != DefaultDays*24*time.Hour {
		t.Errorf("Window(-2) = %v, want the default", got)
	}
}

func TestEventID(t *testing.T) {
	end := now.Add(48 * time.Hour)
	a := EventID(tenant, cust, end)
	if a != EventID(tenant, cust, end.In(time.FixedZone("x", 3*3600))) {
		t.Error("the same instant in another zone is another event id")
	}
	if a == EventID(tenant, cust, end.Add(time.Second)) {
		t.Error("an extended trial has the same event id: it could not be reminded again")
	}
	if a == EventID(tenant, uuid.New(), end) || a == EventID(uuid.New(), cust, end) {
		t.Error("another customer or tenant has the same event id")
	}
}

func TestEvent(t *testing.T) {
	end := now.Add(48 * time.Hour)
	sub := uuid.New()
	ev := Event(tenant, cust, sub, "pro", end, now)
	if ev.GetEventId() != EventID(tenant, cust, end).String() || ev.GetTenantId() != tenant.String() || ev.GetCustomerId() != cust.String() ||
		ev.GetSubscriptionId() != sub.String() || ev.GetPlanCode() != "pro" || !ev.GetTrialEndsAt().AsTime().Equal(end) || !ev.GetDetectedAt().AsTime().Equal(now) {
		t.Errorf("event = %v", ev)
	}
	if got, want := PartitionKey(tenant, cust), tenant.String()+"/"+cust.String(); got != want {
		t.Errorf("PartitionKey = %q, want %q", got, want)
	}
}
