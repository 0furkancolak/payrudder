package query

import (
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// A price change "from a date" moves a subscription on its first period start on or after the date (plan 15), whatever its interval.
func TestNotBeforeMovesToTheFirstPeriodStartOnOrAfterTheDate(t *testing.T) {
	anchor := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 10, 0, 0, 0, time.UTC) }
	priced := func(interval subscription.Interval, next time.Time) *Priced {
		return &Priced{Row: sqlc.GetSubscriptionRow{Interval: string(interval), BillingAnchor: anchor}, Loc: time.UTC, Quote: subscription.Quote{Timing: subscription.AtPeriodEnd, Effective: next, PeriodEnd: next, NewAnchor: next, NewFeeDue: next, NextRenewal: next}}
	}
	for _, c := range []struct {
		name     string
		interval subscription.Interval
		next     time.Time // the next renewal, where the change would start without a date
		from     time.Time
		want     time.Time
	}{
		{"a date between two renewals waits for the next one after it", subscription.Monthly, day(2, 15), day(4, 20), day(5, 15)},
		{"a date on a renewal is that renewal", subscription.Monthly, day(2, 15), day(4, 15), day(4, 15)},
		{"a date one microsecond after a renewal is the next one", subscription.Monthly, day(2, 15), day(4, 15).Add(time.Microsecond), day(5, 15)},
		{"a date before the next renewal changes nothing", subscription.Monthly, day(2, 15), day(2, 1), day(2, 15)},
		{"a yearly subscription waits months for it", subscription.Yearly, time.Date(2027, 1, 15, 10, 0, 0, 0, time.UTC), time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2028, 1, 15, 10, 0, 0, 0, time.UTC)},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := priced(c.interval, c.next)
			if err := p.NotBefore(c.from); err != nil {
				t.Fatal(err)
			}
			q := p.Quote
			if !q.Effective.Equal(c.want) || !q.NewAnchor.Equal(c.want) || !q.NewFeeDue.Equal(c.want) || !q.NextRenewal.Equal(c.want) {
				t.Errorf("effective %v, anchor %v, fee due %v, renewal %v; want %v", q.Effective, q.NewAnchor, q.NewFeeDue, q.NextRenewal, c.want)
			}
			if !q.PeriodEnd.Equal(c.want) {
				t.Errorf("period ends %v, want %v", q.PeriodEnd, c.want)
			}
		})
	}
	t.Run("a change that is not at the end of a period is left alone", func(t *testing.T) {
		p := priced(subscription.Monthly, day(2, 15))
		p.Quote.Timing = subscription.Immediately
		if err := p.NotBefore(day(6, 1)); err != nil || !p.Quote.Effective.Equal(day(2, 15)) {
			t.Errorf("moved an immediate change: %v, %v", p.Quote.Effective, err)
		}
	})
}
