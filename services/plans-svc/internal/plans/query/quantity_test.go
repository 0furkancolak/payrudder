package query

import (
	"math"
	"testing"
)

func TestTimesQuantity(t *testing.T) {
	for _, c := range []struct {
		fee   int64
		seats int32
		want  int64
		ok    bool
	}{
		{3000, 1, 3000, true},
		{3000, 5, 15000, true},
		{3000, 0, 3000, true}, // a row without a quantity is one seat
		{0, 100000, 0, true},
		{math.MaxInt64 / 100000, 100000, math.MaxInt64 / 100000 * 100000, true},
		{math.MaxInt64/100000 + 1, 100000, 0, false},
	} {
		got, err := TimesQuantity(c.fee, c.seats)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("TimesQuantity(%d, %d) = %d, %v; want %d, ok=%v", c.fee, c.seats, got, err, c.want, c.ok)
		}
	}
}
