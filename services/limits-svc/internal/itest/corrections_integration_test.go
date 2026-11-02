//go:build integration

package itest

import (
	"testing"

	"github.com/google/uuid"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
)

// A correction (ADR 0027) carries a signed difference, which a counter of a meter that adds takes: a SUM counter goes down by it, a COUNT counter by one for a cancelled event.
// A correction of a meter that does not add (MAX, LAST, UNIQUE) or has a filter is skipped, whatever it carries.
func testCorrections(t *testing.T) {
	q := newQuotaPod(t)
	tn, cust := uuid.New(), uuid.New()
	fix := func(of *eventsv1.UsageEvent, units int64) *eventsv1.UsageEvent {
		c := q.ev.event(tn, cust, of.MeterCode, units, of.QuantityScale, nil)
		c.Correction = &eventsv1.Correction{CorrectsEventId: of.EventId, Reason: "typo", CorrectedBy: "key:k"}
		return c
	}
	sum := q.ev.event(tn, cust, "api", 10, 0, nil)
	counted := q.ev.event(tn, cust, "calls", 1, 0, nil)
	peak := q.ev.event(tn, cust, "peak", 9, 0, nil)
	seat := q.ev.event(tn, cust, "seats", 5, 0, nil)
	eu := q.ev.event(tn, cust, "eu_api", 4, 0, map[string]string{"region": "eu"})
	q.apply(sum, counted, peak, seat, eu)
	q.apply(fix(sum, -4), fix(counted, -1), fix(peak, -9), fix(seat, -5), fix(eu, -4))

	for meter, want := range map[string]int64{"api": 6, "calls": 0, "peak": 9, "seats": 5, "eu_api": 4} {
		month, _ := keys(tn, cust, meter)
		if got := q.get(month); got != want {
			t.Errorf("%s counter = %d, want %d", meter, got, want)
		}
	}
}
