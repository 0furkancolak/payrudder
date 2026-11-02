package inputs

import (
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
)

func at(day int) time.Time { return time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC) }

func TestLastWithChain(t *testing.T) {
	t.Run("one Subscription is a chain of one", func(t *testing.T) {
		got := lastWithChain([]Subscription{{ID: "a", Start: at(1)}})
		if got.ID != "a" || len(got.Chain) != 1 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a plan change links the subscriptions, oldest first, whatever the order they arrive in", func(t *testing.T) {
		subs := []Subscription{
			{ID: "c", Start: at(20), ChangedFromID: "b"},
			{ID: "a", Start: at(1)},
			{ID: "b", Start: at(10), ChangedFromID: "a"},
		}
		got := lastWithChain(subs)
		if got.ID != "c" || len(got.Chain) != 3 || got.Chain[0].ID != "a" || got.Chain[1].ID != "b" || got.Chain[2].ID != "c" {
			t.Fatalf("got %s chain %v", got.ID, ids(got.Chain))
		}
	})
	t.Run("a new Subscription that did not replace the earlier one is not chained to it", func(t *testing.T) {
		got := lastWithChain([]Subscription{{ID: "a", Start: at(1)}, {ID: "b", Start: at(15)}})
		if got.ID != "b" || len(got.Chain) != 1 {
			t.Fatalf("got %s chain %v", got.ID, ids(got.Chain))
		}
	})
	t.Run("a predecessor outside the period ends the chain", func(t *testing.T) {
		got := lastWithChain([]Subscription{{ID: "b", Start: at(10), ChangedFromID: "gone"}})
		if got.ID != "b" || len(got.Chain) != 1 {
			t.Fatalf("got %s chain %v", got.ID, ids(got.Chain))
		}
	})
}

func ids(s []Subscription) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.ID
	}
	return out
}

// The fixed fee of a period is charged from the end of a trial that ends inside it, when the plan version prorates after a trial; a version that does not, a trial that
// ended before the period and one that runs past it do not move the start (docs/code-quality/billing-svc-status.md 17).
func TestFeeFromIsTheEndOfATrialInsideThePeriod(t *testing.T) {
	start, end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	trial := func(d time.Time) Subscription { return Subscription{TrialEnd: &d} }
	prorates, whole := rating.PlanVersion{ProrateAfterTrial: true}, rating.PlanVersion{}
	for _, tc := range []struct {
		name string
		sub  Subscription
		pv   rating.PlanVersion
		want time.Time
	}{
		{"a trial that ends inside the period", trial(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)), prorates, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)},
		{"an older version charges the whole period", trial(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)), whole, start},
		{"no trial", Subscription{}, prorates, start},
		{"a trial that ended before the period", trial(time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)), prorates, start},
		{"a trial that ended at the start of the period", trial(start), prorates, start},
		{"a trial that ends at the end of the period: nothing to charge", trial(end), prorates, end},
		{"a trial that runs past the period is waived elsewhere", trial(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)), prorates, start},
	} {
		if got := FeeFrom(tc.sub, tc.pv, start, end); !got.Equal(tc.want) {
			t.Errorf("%s: FeeFrom = %s, want %s", tc.name, got.Format(time.RFC3339), tc.want.Format(time.RFC3339))
		}
	}
}
