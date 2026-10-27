package domain

import (
	"math/big"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/usage"
)

// The merges below are what the upserts of the aggregator do in SQL. Whatever order a log reaches the aggregator in (partitions, redeliveries,
// a replay), the stored row must be the same, so each merge is checked for the laws that make that true.

var base = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func event(id string, offset time.Duration, units int64) usage.Event {
	return usage.Event{EventID: id, EventTime: base.Add(offset), Units: units, Scale: 2}
}

func cellOf(m usage.Meter, evs ...usage.Event) *Cell {
	c := &Cell{}
	for _, e := range evs {
		c.add(m, e)
	}
	return c
}

func same(a, b *Cell) bool {
	return a.Value.Cmp(b.Value) == 0 && a.Events == b.Events && a.Order == b.Order && a.Scale == b.Scale
}

func randomEvents(r *rand.Rand, n int) []usage.Event {
	evs := make([]usage.Event, n)
	for i := range evs {
		// Few distinct times so that the tie-break by id is exercised, and unique ids so that no two events compare equal.
		evs[i] = event("ev"+string(rune('A'+i/26))+string(rune('a'+i%26)), time.Duration(r.IntN(4))*time.Second, int64(r.IntN(1000)))
	}
	return evs
}

func TestCellMergeLaws(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, agg := range []usage.Aggregation{usage.Sum, usage.Count, usage.Max, usage.Last} {
		m := usage.Meter{Code: "m", Aggregation: agg}
		for range 200 {
			evs := randomEvents(r, 2+r.IntN(30))
			whole := cellOf(m, evs...)

			// Any partition of the events into three groups, merged in any order, is the cell of all of them (associativity and commutativity).
			groups := [3][]usage.Event{}
			for _, e := range evs {
				g := r.IntN(3)
				groups[g] = append(groups[g], e)
			}
			var cells []*Cell
			for _, g := range groups {
				if len(g) > 0 {
					cells = append(cells, cellOf(m, g...))
				}
			}
			r.Shuffle(len(cells), func(i, j int) { cells[i], cells[j] = cells[j], cells[i] })
			var merged *Cell
			for _, c := range cells {
				merged = Merge(merged, c)
			}
			if !same(merged, whole) {
				t.Fatalf("%v: merged %v/%v/%d != whole %v/%v/%d", agg, merged.Value, merged.Order, merged.Events, whole.Value, whole.Order, whole.Events)
			}
			if len(cells) == 3 {
				left, right := Merge(Merge(cells[0], cells[1]), cells[2]), Merge(cells[0], Merge(cells[1], cells[2]))
				if !same(left, right) {
					t.Fatalf("%v: (a·b)·c != a·(b·c)", agg)
				}
			}
			// The same event delivered twice cannot move a MAX or a LAST (the dedup in front of the upsert is what protects SUM).
			if agg == usage.Max || agg == usage.Last {
				twice := Merge(whole, cellOf(m, evs[0]))
				if twice.Value.Cmp(whole.Value) != 0 || twice.Order != whole.Order {
					t.Fatalf("%v: a repeated event moved the value", agg)
				}
			}
		}
	}
}

func TestLastIsTheLatestEventByTimeThenID(t *testing.T) {
	m := usage.Meter{Code: "seats", Aggregation: usage.Last}
	cases := []struct {
		name string
		evs  []usage.Event
		want int64
	}{
		{"later time wins whatever the arrival order", []usage.Event{event("b", time.Second, 7), event("a", 0, 9)}, 7},
		{"and the other arrival order", []usage.Event{event("a", 0, 9), event("b", time.Second, 7)}, 7},
		{"equal times: the greater id wins", []usage.Event{event("a", 0, 1), event("c", 0, 3), event("b", 0, 2)}, 3},
		{"ids compare bytewise, not by locale", []usage.Event{event("Z", 0, 1), event("a", 0, 2)}, 2},
		{"a smaller value can be the last one", []usage.Event{event("a", 0, 100), event("b", time.Second, 4)}, 4},
		{"microsecond precision is what orders", []usage.Event{event("b", time.Microsecond, 5), event("z", 0, 6)}, 5},
	}
	for _, c := range cases {
		if got := cellOf(m, c.evs...).Value.Int64(); got != c.want {
			t.Errorf("%s: last = %d, want %d", c.name, got, c.want)
		}
	}
	// Sub-microsecond differences are below what Postgres keeps, so they must not order anything: both events are the same instant.
	a, b := event("a", 0, 1), event("b", 0, 2)
	b.EventTime = b.EventTime.Add(500 * time.Nanosecond)
	if got := cellOf(m, a, b).Value.Int64(); got != 2 {
		t.Errorf("same microsecond falls back to the id: got %d, want 2", got)
	}
}

func TestUniqueCellCountsNoQuantity(t *testing.T) {
	m := usage.Meter{Code: "users", Aggregation: usage.Unique, ValueProperty: "user"}
	c := cellOf(m, event("a", 0, 5), event("b", 0, 9))
	if c.Value.Sign() != 0 || c.Events != 2 || c.Scale != 0 {
		t.Fatalf("a UNIQUE cell before the distinct step: value %v events %d scale %d", c.Value, c.Events, c.Scale)
	}
	d := NewDelta()
	k := OnTimeKey{Tenant: uuid.New(), Customer: uuid.New(), Meter: "users", Period: "2026-11", Generation: 1}
	d.AddOnTime(k, m, event("a", 0, 5))
	d.AddDistinctOnTime(k, 3)
	d.AddDistinctOnTime(k, -1)
	if got := d.OnTime[k].Value; got.Cmp(big.NewInt(2)) != 0 || d.OnTime[k].Events != 1 {
		t.Fatalf("value %v events %d, want 2 and 1", got, d.OnTime[k].Events)
	}
	lk := LateKey{Tenant: k.Tenant, Customer: k.Customer, Meter: "users", Billed: "2026-12", Origin: "2026-11", Generation: 1}
	d.AddDistinctLate(lk, -1)
	if d.Late[lk].Value.Int64() != -1 || d.Late[lk].Events != 0 {
		t.Fatalf("a late row touched only by a conversion: %v events %d", d.Late[lk].Value, d.Late[lk].Events)
	}
}

func TestMergeSeenLaws(t *testing.T) {
	states := []string{"", "2026-11", "2026-12", "2027-01"}
	for _, a := range states {
		if MergeSeen(a, a) != a {
			t.Errorf("not idempotent at %q", a)
		}
		for _, b := range states {
			if MergeSeen(a, b) != MergeSeen(b, a) {
				t.Errorf("not commutative at %q, %q", a, b)
			}
			for _, c := range states {
				if MergeSeen(MergeSeen(a, b), c) != MergeSeen(a, MergeSeen(b, c)) {
					t.Errorf("not associative at %q, %q, %q", a, b, c)
				}
			}
		}
	}
	// An on-time event always wins, and otherwise the earliest billing period does.
	if MergeSeen("2026-12", "") != "" || MergeSeen("2026-12", "2026-11") != "2026-11" {
		t.Error("on time must beat late, and an earlier billing period a later one")
	}
}

func TestDeltaSeenIsOrderIndependent(t *testing.T) {
	k := SeenKey{Tenant: uuid.New(), Customer: uuid.New(), Meter: "users", Period: "2026-10", Generation: 1, Value: usage.Digest("alice")}
	feed := [][]string{{"", "2026-11", "2026-12"}, {"2026-12", "2026-11", ""}, {"2026-11", "", "2026-12"}}
	for _, order := range feed {
		d := NewDelta()
		for _, b := range order {
			d.AddSeen(k, b)
		}
		if d.Seen[k] != "" {
			t.Errorf("order %v ended as %q, want on time", order, d.Seen[k])
		}
	}
	d := NewDelta()
	d.AddSeen(k, "2026-12")
	d.AddSeen(k, "2026-11")
	if d.Seen[k] != "2026-11" {
		t.Errorf("late only: %q, want the earliest billing period", d.Seen[k])
	}
}

func TestSortedSeenIsStable(t *testing.T) {
	tn, c := uuid.New(), uuid.New()
	d := NewDelta()
	for _, v := range []string{"c", "a", "b"} {
		d.AddSeen(SeenKey{Tenant: tn, Customer: c, Meter: "m", Period: "2026-11", Generation: 1, Value: usage.Digest(v)}, "")
	}
	first := SortedSeen(d.Seen)
	for range 10 {
		again := SortedSeen(d.Seen)
		for i := range first {
			if first[i] != again[i] {
				t.Fatal("the order of the keys changes between calls")
			}
		}
	}
}
