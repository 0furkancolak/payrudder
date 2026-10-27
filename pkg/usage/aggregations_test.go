package usage

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func TestFromProtoUniqueAndLast(t *testing.T) {
	u := FromProto(&plansv1.Meter{Code: "users", Aggregation: plansv1.Aggregation_AGGREGATION_UNIQUE, ValueProperty: "user_id"})
	if u.Aggregation != Unique || u.ValueProperty != "user_id" {
		t.Fatalf("UNIQUE meter = %+v", u)
	}
	l := FromProto(&plansv1.Meter{Code: "seats", Aggregation: plansv1.Aggregation_AGGREGATION_LAST, ValueProperty: "n", QuantityScale: 2})
	if l.Aggregation != Last || l.Scale != 2 {
		t.Fatalf("LAST meter = %+v", l)
	}
}

func TestAdditive(t *testing.T) {
	for agg, want := range map[Aggregation]bool{Sum: true, Count: true, Max: false, Unique: false, Last: false} {
		if agg.Additive() != want {
			t.Errorf("Additive(%d) = %v, want %v", agg, agg.Additive(), want)
		}
	}
}

func TestValueOfUniqueAndLast(t *testing.T) {
	e := Event{Units: 42, Props: map[string]string{"user_id": "alice"}}
	if v := (Meter{Aggregation: Unique, ValueProperty: "user_id"}).Value(e); v != 0 {
		t.Errorf("a UNIQUE event has no quantity, got %d", v)
	}
	if v := (Meter{Aggregation: Last}).Value(e); v != 42 {
		t.Errorf("a LAST event's quantity is its own, got %d", v)
	}
}

func TestDistinctValue(t *testing.T) {
	m := Meter{Aggregation: Unique, ValueProperty: "user_id"}
	for _, c := range []struct {
		props map[string]string
		want  string
		ok    bool
	}{
		{map[string]string{"user_id": "alice"}, "alice", true},
		{map[string]string{"user_id": ""}, "", false},
		{map[string]string{"other": "x"}, "", false},
		{nil, "", false},
	} {
		if v, ok := m.DistinctValue(Event{Props: c.props}); v != c.want || ok != c.ok {
			t.Errorf("DistinctValue(%v) = %q, %v; want %q, %v", c.props, v, ok, c.want, c.ok)
		}
	}
	if _, ok := (Meter{Aggregation: Unique}).DistinctValue(Event{Props: map[string]string{"": "x"}}); ok {
		t.Error("a meter with no value property has no distinct value")
	}
	first, again := Digest("alice"), Digest("alice")
	if first != again || first == Digest("Alice") || Digest("") == Digest(" ") {
		t.Error("digest must separate values and be stable")
	}
}

func TestOrderIsATotalOrder(t *testing.T) {
	base := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	mk := func(offset time.Duration, id string) Order {
		return OrderOf(Event{EventTime: base.Add(offset), EventID: id})
	}
	cases := []struct {
		a, b Order
		want int
	}{
		{mk(0, "a"), mk(time.Second, "a"), -1},
		{mk(time.Second, "a"), mk(0, "z"), 1},
		{mk(0, "a"), mk(0, "b"), -1},
		{mk(0, "B"), mk(0, "a"), -1}, // bytewise: upper case sorts before lower case in every locale of a database
		{mk(0, "a"), mk(0, "a"), 0},
		{mk(time.Microsecond, "a"), mk(0, "z"), 1},
		{OrderOf(Event{EventTime: base.Add(900 * time.Nanosecond), EventID: "a"}), mk(0, "b"), -1}, // sub-microsecond time is dropped, then the id decides
	}
	for _, c := range cases {
		if got := c.a.Compare(c.b); got != c.want {
			t.Errorf("Compare(%v, %v) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := c.b.Compare(c.a); got != -c.want {
			t.Errorf("Compare is not antisymmetric for %v, %v", c.a, c.b)
		}
	}
	// Sorting any permutation gives one sequence, and "latest" is its end: the property the aggregator and limits-svc rely on to agree.
	r := rand.New(rand.NewPCG(3, 4))
	var orders []Order
	for i := range 40 {
		orders = append(orders, mk(time.Duration(r.IntN(5))*time.Microsecond, string(rune('a'+r.IntN(5)))+string(rune('0'+i%10))))
	}
	want := slices.Clone(orders)
	slices.SortFunc(want, Order.Compare)
	for range 20 {
		got := slices.Clone(orders)
		r.Shuffle(len(got), func(i, j int) { got[i], got[j] = got[j], got[i] })
		slices.SortFunc(got, Order.Compare)
		if !slices.Equal(got, want) {
			t.Fatal("sorting depends on the starting order")
		}
	}
}
