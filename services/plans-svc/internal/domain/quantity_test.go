package domain

import "testing"

func TestQuantityOf(t *testing.T) {
	for _, c := range []struct {
		name               string
		requested, current int32
		want               int32
		ok                 bool
	}{
		{"zero keeps the current", 0, 7, 7, true},
		{"one", 1, 7, 1, true},
		{"the most", MaxQuantity, 1, MaxQuantity, true},
		{"above the most", MaxQuantity + 1, 1, 0, false},
		{"negative", -3, 1, 0, false},
	} {
		got, err := QuantityOf(c.requested, c.current)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("%s: QuantityOf(%d, %d) = %d, %v; want %d, ok=%v", c.name, c.requested, c.current, got, err, c.want, c.ok)
		}
	}
}
