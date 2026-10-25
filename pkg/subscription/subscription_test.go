package subscription_test

import (
	"errors"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/subscription"
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

func TestYearlyPeriodsFollowTheAnniversary(t *testing.T) {
	loc := time.UTC
	anchor := time.Date(2028, 2, 29, 10, 0, 0, 0, loc)
	for _, tc := range []struct {
		at        time.Time
		wantIndex int
		wantStart time.Time
		wantEnd   time.Time
	}{
		{anchor, 0, anchor, time.Date(2029, 2, 28, 10, 0, 0, 0, loc)},
		{time.Date(2029, 2, 27, 23, 0, 0, 0, loc), 0, anchor, time.Date(2029, 2, 28, 10, 0, 0, 0, loc)},
		{time.Date(2029, 2, 28, 10, 0, 0, 0, loc), 1, time.Date(2029, 2, 28, 10, 0, 0, 0, loc), time.Date(2030, 2, 28, 10, 0, 0, 0, loc)},
		// Four years on, the day of the month comes back: the periods are counted from the anchor, they do not drift.
		{time.Date(2032, 2, 29, 10, 0, 0, 0, loc), 4, time.Date(2032, 2, 29, 10, 0, 0, 0, loc), time.Date(2033, 2, 28, 10, 0, 0, 0, loc)},
	} {
		n, err := subscription.PeriodIndex(anchor, subscription.Yearly, tc.at, loc)
		if err != nil || n != tc.wantIndex {
			t.Fatalf("PeriodIndex(%s) = %d, %v; want %d", tc.at, n, err, tc.wantIndex)
		}
		start, _ := subscription.PeriodStart(anchor, subscription.Yearly, n, loc)
		end, _ := subscription.PeriodEnd(anchor, subscription.Yearly, n, loc)
		if !start.Equal(tc.wantStart) || !end.Equal(tc.wantEnd) {
			t.Errorf("period %d = %s – %s; want %s – %s", n, start, end, tc.wantStart, tc.wantEnd)
		}
	}
}

func TestQuoteChange(t *testing.T) {
	loc := time.UTC
	anchor := time.Date(2026, 10, 1, 0, 0, 0, 0, loc) // October has 31 days
	mid := time.Date(2026, 10, 16, 0, 0, 0, 0, loc)   // 15 of 31 days used
	month := func(fee int64) subscription.Plan {
		return subscription.Plan{FeeMinor: fee, Interval: subscription.Monthly}
	}
	year := func(fee int64) subscription.Plan {
		return subscription.Plan{FeeMinor: fee, Interval: subscription.Yearly}
	}
	nov1 := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)

	t.Run("an upgrade is immediate and credits the unused old time against the rest of the new plan", func(t *testing.T) {
		q, err := subscription.QuoteChange(subscription.ChangeRequest{Old: month(3100), New: month(9300), Anchor: anchor, At: mid, Loc: loc})
		if err != nil {
			t.Fatal(err)
		}
		// 16 of 31 days are left: 3100 × 16/31 = 1600 credit, 9300 × 16/31 = 4800 charge.
		if q.Timing != subscription.Immediately || q.Proration != subscription.Prorate || q.CreditMinor != 1600 || q.ChargeMinor != 4800 || q.NetMinor != 3200 {
			t.Fatalf("quote = %+v", q)
		}
		if !q.NewAnchor.Equal(anchor) || !q.NewFeeDue.Equal(nov1) || q.NextRenewalMinor != 9300 {
			t.Errorf("the cycle must be kept: %+v", q)
		}
	})
	t.Run("a downgrade waits for the end of the period and moves no money", func(t *testing.T) {
		q, err := subscription.QuoteChange(subscription.ChangeRequest{Old: month(9300), New: month(3100), Anchor: anchor, At: mid, Loc: loc})
		if err != nil {
			t.Fatal(err)
		}
		if q.Timing != subscription.AtPeriodEnd || q.NetMinor != 0 || !q.Effective.Equal(nov1) || !q.NewAnchor.Equal(nov1) || !q.NewFeeDue.Equal(nov1) || q.NextRenewalMinor != 3100 {
			t.Fatalf("quote = %+v", q)
		}
	})
	t.Run("a downgrade can be made immediate: the difference becomes credit", func(t *testing.T) {
		q, err := subscription.QuoteChange(subscription.ChangeRequest{Old: month(9300), New: month(3100), Anchor: anchor, At: mid, Loc: loc, Timing: subscription.Immediately})
		if err != nil {
			t.Fatal(err)
		}
		if q.NetMinor != 1600-4800 || q.CreditMinor != 4800 || q.ChargeMinor != 1600 {
			t.Fatalf("quote = %+v", q)
		}
	})
	t.Run("monthly to yearly starts the year now and credits the unused month", func(t *testing.T) {
		q, err := subscription.QuoteChange(subscription.ChangeRequest{Old: month(3100), New: year(31000), Anchor: anchor, At: mid, Loc: loc})
		if err != nil {
			t.Fatal(err)
		}
		if q.Timing != subscription.Immediately || q.CreditMinor != 1600 || q.ChargeMinor != 31000 || q.NetMinor != 29400 ||
			!q.NewAnchor.Equal(mid) || !q.NewFeeDue.Equal(time.Date(2027, 10, 16, 0, 0, 0, 0, loc)) {
			t.Fatalf("quote = %+v", q)
		}
	})
	t.Run("yearly to monthly waits for the end of the paid year", func(t *testing.T) {
		q, err := subscription.QuoteChange(subscription.ChangeRequest{Old: year(31000), New: month(3100), Anchor: anchor, At: mid, Loc: loc})
		if err != nil {
			t.Fatal(err)
		}
		if q.Timing != subscription.AtPeriodEnd || !q.Effective.Equal(time.Date(2027, 10, 1, 0, 0, 0, 0, loc)) || q.NetMinor != 0 {
			t.Fatalf("quote = %+v", q)
		}
	})
	t.Run("yearly to monthly now credits the unused year, which exceeds the first month", func(t *testing.T) {
		q, err := subscription.QuoteChange(subscription.ChangeRequest{Old: year(36500), New: month(3100), Anchor: anchor, At: time.Date(2027, 4, 2, 0, 0, 0, 0, loc), Loc: loc, Timing: subscription.Immediately})
		if err != nil {
			t.Fatal(err)
		}
		// 2027-04-02 to 2027-10-01 is 182 of 365 days.
		if q.CreditMinor != 18200 || q.ChargeMinor != 3100 || q.NetMinor != 3100-18200 || !q.NewAnchor.Equal(time.Date(2027, 4, 2, 0, 0, 0, 0, loc)) {
			t.Fatalf("quote = %+v", q)
		}
	})
	t.Run("full starts a new cycle in the same interval", func(t *testing.T) {
		q, err := subscription.QuoteChange(subscription.ChangeRequest{Old: month(3100), New: month(9300), Anchor: anchor, At: mid, Loc: loc, Proration: subscription.Full})
		if err != nil {
			t.Fatal(err)
		}
		if q.CreditMinor != 1600 || q.ChargeMinor != 9300 || !q.NewAnchor.Equal(mid) || !q.NewFeeDue.Equal(time.Date(2026, 11, 16, 0, 0, 0, 0, loc)) {
			t.Fatalf("quote = %+v", q)
		}
	})
	t.Run("no proration changes the plan with no money and bills the new fee from the next period", func(t *testing.T) {
		q, err := subscription.QuoteChange(subscription.ChangeRequest{Old: month(3100), New: month(9300), Anchor: anchor, At: mid, Loc: loc, Proration: subscription.NoProration})
		if err != nil {
			t.Fatal(err)
		}
		if q.Timing != subscription.Immediately || q.NetMinor != 0 || q.CreditMinor != 0 || q.ChargeMinor != 0 || !q.NewFeeDue.Equal(nov1) || q.NextRenewalMinor != 9300 {
			t.Fatalf("quote = %+v", q)
		}
	})
	t.Run("refusals", func(t *testing.T) {
		for name, r := range map[string]subscription.ChangeRequest{
			"before the anchor":                     {Old: month(1), New: month(2), Anchor: anchor, At: anchor.Add(-time.Hour)},
			"no proration across intervals":         {Old: month(1), New: year(2), Anchor: anchor, At: mid, Proration: subscription.NoProration},
			"proration with a change at period end": {Old: month(1), New: month(2), Anchor: anchor, At: mid, Timing: subscription.AtPeriodEnd, Proration: subscription.Prorate},
			"unknown timing":                        {Old: month(1), New: month(2), Anchor: anchor, At: mid, Timing: "later"},
			"unknown proration":                     {Old: month(1), New: month(2), Anchor: anchor, At: mid, Proration: "half"},
			"negative fee":                          {Old: month(-1), New: month(2), Anchor: anchor, At: mid},
		} {
			r.Loc = loc
			if _, err := subscription.QuoteChange(r); !errors.Is(err, subscription.ErrBadChange) {
				t.Errorf("%s: err = %v", name, err)
			}
		}
	})
	t.Run("a change at the very start of the period credits the whole old fee", func(t *testing.T) {
		q, err := subscription.QuoteChange(subscription.ChangeRequest{Old: month(3100), New: month(9300), Anchor: anchor, At: anchor, Loc: loc})
		if err != nil || q.CreditMinor != 3100 || q.ChargeMinor != 9300 || q.NetMinor != 6200 {
			t.Fatalf("quote = %+v, %v", q, err)
		}
	})
}

func TestCouponDiscount(t *testing.T) {
	pct := func(bps int32, dur string, n int32) subscription.CouponTerms {
		return subscription.CouponTerms{PercentBPS: bps, Duration: dur, DurationPeriods: n}
	}
	amt := func(a int64, cur, dur string, n int32) subscription.CouponTerms {
		return subscription.CouponTerms{AmountMinor: a, Currency: cur, Duration: dur, DurationPeriods: n}
	}
	for _, tc := range []struct {
		name    string
		fee     int64
		c       subscription.CouponTerms
		cur     string
		applied int32
		want    int64
	}{
		{"10 percent of 1999 rounds half even", 1999, pct(1000, "forever", 0), "USD", 7, 200},
		{"12.5 percent of 100", 100, pct(1250, "once", 0), "USD", 0, 12},
		{"12.5 percent of 108 is 13.5, rounded to even 14", 108, pct(1250, "once", 0), "USD", 0, 14},
		{"12.5 percent of 104 is 13, exactly", 104, pct(1250, "once", 0), "USD", 0, 13},
		{"100 percent is the whole fee", 500, pct(10000, "forever", 0), "USD", 0, 500},
		{"once applies to the first fee only", 1000, pct(5000, "once", 0), "USD", 1, 0},
		{"repeating applies for its periods", 1000, pct(5000, "repeating", 3), "USD", 2, 500},
		{"repeating is used up", 1000, pct(5000, "repeating", 3), "USD", 3, 0},
		{"an amount is taken off", 1000, amt(300, "USD", "forever", 0), "USD", 9, 300},
		{"an amount is never more than the fee", 200, amt(300, "USD", "forever", 0), "USD", 0, 200},
		{"an amount in another currency does not apply", 1000, amt(300, "EUR", "forever", 0), "USD", 0, 0},
		{"no fee, no discount", 0, pct(5000, "forever", 0), "USD", 0, 0},
		{"unknown duration", 1000, pct(5000, "sometimes", 0), "USD", 0, 0},
	} {
		if got := subscription.CouponDiscount(tc.fee, tc.c, tc.cur, tc.applied); got != tc.want {
			t.Errorf("%s: discount = %d; want %d", tc.name, got, tc.want)
		}
	}
}
