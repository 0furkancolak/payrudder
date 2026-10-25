package page

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSize(t *testing.T) {
	for _, c := range []struct {
		req, def, max, want int32
		ok                  bool
	}{
		{0, 50, 200, 50, true}, {1, 50, 200, 1, true}, {200, 50, 200, 200, true},
		{201, 50, 200, 0, false}, {-1, 50, 200, 0, false}, {1 << 30, 100, 500, 0, false},
	} {
		got, ok := Size(c.req, c.def, c.max)
		if got != c.want || ok != c.ok {
			t.Errorf("Size(%d, %d, %d) = %d, %v; want %d, %v", c.req, c.def, c.max, got, ok, c.want, c.ok)
		}
	}
}

func TestTrim(t *testing.T) {
	if p, more := Trim([]int{1, 2, 3}, 2); len(p) != 2 || !more {
		t.Errorf("Trim of size+1 rows = %v, %v", p, more)
	}
	if p, more := Trim([]int{1, 2}, 2); len(p) != 2 || more {
		t.Errorf("Trim of size rows = %v, %v", p, more)
	}
	if p, more := Trim[int](nil, 2); len(p) != 0 || more {
		t.Errorf("Trim of nothing = %v, %v", p, more)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	c := Cursor{At: time.Date(2026, 10, 25, 21, 0, 0, 123456000, time.UTC), ID: uuid.New()}
	got, ok := ParseCursor(c.Token())
	if !ok || !got.At.Equal(c.At) || got.ID != c.ID {
		t.Fatalf("round trip = %+v, %v", got, ok)
	}
	for _, bad := range []string{"", "x", "2026-10-25T21:00:00Z", "|", "nope|" + c.ID.String(), c.At.Format(time.RFC3339Nano) + "|nope", c.At.Format(time.RFC3339Nano)} {
		if _, ok := ParseCursor(bad); ok {
			t.Errorf("ParseCursor(%q) accepted", bad)
		}
	}
	if s := Start(); !s.At.After(c.At) || s.ID != uuid.Max {
		t.Errorf("Start = %+v", s)
	}
}
