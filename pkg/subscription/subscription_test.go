package subscription_test

import (
	"errors"
	"testing"
	"time"

	"github.com/0furkancolak/quotify/pkg/subscription"
)

func TestNext(t *testing.T) {
	cases := []struct {
		from subscription.State
		ev   subscription.Event
		to   subscription.State
		ok   bool
	}{
		{subscription.Trialing, subscription.TrialEnded, subscription.Active, true},
		{subscription.Active, subscription.PaymentFailed, subscription.PastDue, true},
		{subscription.PastDue, subscription.PaymentSucceeded, subscription.Active, true},
		{subscription.PastDue, subscription.DunningExhausted, subscription.Cancelled, true},
		{subscription.Active, subscription.Pause, subscription.Paused, true},
		{subscription.Paused, subscription.Resume, subscription.Active, true},
		{subscription.Active, subscription.PeriodEndedCancelling, subscription.Cancelled, true},
		{subscription.Paused, subscription.PaymentSucceeded, subscription.Paused, false},
		{subscription.Cancelled, subscription.Resume, subscription.Cancelled, false},
		{subscription.Trialing, subscription.Pause, subscription.Trialing, false},
	}
	for _, c := range cases {
		got, err := subscription.Next(c.from, c.ev)
		if c.ok != (err == nil) || got != c.to {
			t.Errorf("Next(%s,%s) = %s,%v; want %s ok=%v", c.from, c.ev, got, err, c.to, c.ok)
		}
		var inv *subscription.ErrInvalidTransition
		if !c.ok && !errors.As(err, &inv) {
			t.Errorf("Next(%s,%s): error %v is not ErrInvalidTransition", c.from, c.ev, err)
		}
	}
}

func TestCancelledIsTerminalAndEveryStateCanBeCancelledOrIsTerminal(t *testing.T) {
	for _, s := range []subscription.State{subscription.Trialing, subscription.Active, subscription.PastDue, subscription.Paused} {
		if subscription.Terminal(s) {
			t.Errorf("%s must not be terminal", s)
		}
		if to, err := subscription.Next(s, subscription.Cancel); err != nil || to != subscription.Cancelled {
			t.Errorf("%s cannot be cancelled: %v", s, err)
		}
	}
	if !subscription.Terminal(subscription.Cancelled) {
		t.Error("cancelled must be terminal")
	}
}

func TestBillable(t *testing.T) {
	for s, want := range map[subscription.State]bool{
		subscription.Trialing: false, subscription.Active: true, subscription.PastDue: true,
		subscription.Paused: false, subscription.Cancelled: false,
	} {
		if got := subscription.Billable(s); got != want {
			t.Errorf("Billable(%s) = %v", s, got)
		}
	}
}

func TestPeriodEndClampsTheDayWithoutDrifting(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Istanbul")
	anchor := time.Date(2027, time.January, 31, 9, 0, 0, 0, loc)
	want := []time.Time{
		time.Date(2027, time.February, 28, 9, 0, 0, 0, loc),
		time.Date(2027, time.March, 31, 9, 0, 0, 0, loc),
		time.Date(2027, time.April, 30, 9, 0, 0, 0, loc),
		time.Date(2027, time.May, 31, 9, 0, 0, 0, loc),
	}
	for n, w := range want {
		got, err := subscription.PeriodEnd(anchor, subscription.Monthly, n, loc)
		if err != nil || !got.Equal(w) {
			t.Errorf("period %d ends %s (%v); want %s", n, got, err, w)
		}
	}
	y, _ := subscription.PeriodEnd(time.Date(2028, 2, 29, 0, 0, 0, 0, loc), subscription.Yearly, 0, loc)
	if !y.Equal(time.Date(2029, 2, 28, 0, 0, 0, 0, loc)) {
		t.Errorf("yearly from a leap day ends %s", y)
	}
}

func TestPeriodBoundariesTile(t *testing.T) {
	loc := time.UTC
	anchor := time.Date(2027, 1, 31, 0, 0, 0, 0, loc)
	for n := 1; n < 30; n++ {
		start, _ := subscription.PeriodStart(anchor, subscription.Monthly, n, loc)
		prev, _ := subscription.PeriodEnd(anchor, subscription.Monthly, n-1, loc)
		if !start.Equal(prev) {
			t.Fatalf("period %d start %s != previous end %s", n, start, prev)
		}
	}
	idx, err := subscription.PeriodIndex(anchor, subscription.Monthly, time.Date(2027, 3, 31, 0, 0, 0, 0, loc), loc)
	if err != nil || idx != 2 {
		t.Errorf("PeriodIndex = %d, %v; want 2", idx, err)
	}
	if _, err := subscription.PeriodIndex(anchor, subscription.Monthly, anchor.Add(-time.Second), loc); err == nil {
		t.Error("an instant before the anchor must be an error")
	}
}

func TestPeriodEndRejectsBadInput(t *testing.T) {
	if _, err := subscription.PeriodEnd(time.Now(), "week", 0, time.UTC); err == nil {
		t.Error("unknown interval must be an error")
	}
	if _, err := subscription.PeriodEnd(time.Now(), subscription.Monthly, -1, time.UTC); err == nil {
		t.Error("negative index must be an error")
	}
}

func TestPortion(t *testing.T) {
	start := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC) // 30 days
	mid := start.Add(15 * 24 * time.Hour)

	whole, _ := subscription.Portion(3000, start, end, start, end)
	if whole != 3000 {
		t.Errorf("a segment covering the period costs %d; want the whole fee", whole)
	}
	first, _ := subscription.Portion(3000, start, mid, start, end)
	second, _ := subscription.Portion(9000, mid, end, start, end)
	if first != 1500 || second != 4500 {
		t.Errorf("upgrade at the midpoint = %d + %d; want 1500 + 4500", first, second)
	}
	empty, _ := subscription.Portion(3000, mid, mid, start, end)
	if empty != 0 {
		t.Errorf("an empty segment costs %d", empty)
	}
}

func TestPortionRoundsHalfEvenOnce(t *testing.T) {
	start := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Second)
	at := start.Add(time.Second) // exactly half
	for fee, want := range map[int64]int64{1: 0, 3: 2, 5: 2, 7: 4, 100: 50} {
		got, _ := subscription.Portion(fee, start, at, start, end)
		if got != want {
			t.Errorf("half of %d = %d; want %d (half-even)", fee, got, want)
		}
	}
}

func TestPortionRejectsBadInput(t *testing.T) {
	s := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	e := s.Add(time.Hour)
	for name, f := range map[string]func() error{
		"negative fee":   func() error { _, err := subscription.Portion(-1, s, e, s, e); return err },
		"empty period":   func() error { _, err := subscription.Portion(1, s, s, s, s); return err },
		"before period":  func() error { _, err := subscription.Portion(1, s.Add(-time.Second), e, s, e); return err },
		"after period":   func() error { _, err := subscription.Portion(1, s, e.Add(time.Second), s, e); return err },
		"reversed range": func() error { _, err := subscription.Portion(1, e, s, s, e); return err },
	} {
		if !errors.Is(f(), subscription.ErrBadPortion) {
			t.Errorf("%s: want ErrBadPortion", name)
		}
	}
}
