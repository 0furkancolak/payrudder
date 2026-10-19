package runs

import (
	"testing"
	"time"
)

func at(day int) time.Time { return time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC) }

func TestLastWithChain(t *testing.T) {
	t.Run("one subscription is a chain of one", func(t *testing.T) {
		got := lastWithChain([]subscription{{ID: "a", Start: at(1)}})
		if got.ID != "a" || len(got.Chain) != 1 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a plan change links the subscriptions, oldest first, whatever the order they arrive in", func(t *testing.T) {
		subs := []subscription{
			{ID: "c", Start: at(20), ChangedFromID: "b"},
			{ID: "a", Start: at(1)},
			{ID: "b", Start: at(10), ChangedFromID: "a"},
		}
		got := lastWithChain(subs)
		if got.ID != "c" || len(got.Chain) != 3 || got.Chain[0].ID != "a" || got.Chain[1].ID != "b" || got.Chain[2].ID != "c" {
			t.Fatalf("got %s chain %v", got.ID, ids(got.Chain))
		}
	})
	t.Run("a new subscription that did not replace the earlier one is not chained to it", func(t *testing.T) {
		got := lastWithChain([]subscription{{ID: "a", Start: at(1)}, {ID: "b", Start: at(15)}})
		if got.ID != "b" || len(got.Chain) != 1 {
			t.Fatalf("got %s chain %v", got.ID, ids(got.Chain))
		}
	})
	t.Run("a predecessor outside the period ends the chain", func(t *testing.T) {
		got := lastWithChain([]subscription{{ID: "b", Start: at(10), ChangedFromID: "gone"}})
		if got.ID != "b" || len(got.Chain) != 1 {
			t.Fatalf("got %s chain %v", got.ID, ids(got.Chain))
		}
	})
}

func ids(s []subscription) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.ID
	}
	return out
}
