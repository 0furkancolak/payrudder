package engine

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/period"
)

// Every kind of counter key parses back to the ref it was made from, and the prefix says how the key is watched: lim:c and lim:y are reconciled
// from lim:active, lim:w from lim:wactive.
func TestKeysRoundTrip(t *testing.T) {
	tn, cust := uuid.New(), uuid.New()
	cases := []struct {
		name     string
		ref      Ref
		prefix   string
		windowed bool
	}{
		{"month", Ref{Tenant: tn, Customer: cust, Meter: "api", Period: "2026-11"}, "lim:c:", false},
		{"hour", Ref{Tenant: tn, Customer: cust, Meter: "api", Period: "2026-11-05T13", Windowed: true}, "lim:w:", true},
		{"day", Ref{Tenant: tn, Customer: cust, Meter: "api", Period: "2026-11-05", Windowed: true}, "lim:w:", true},
		{"week", Ref{Tenant: tn, Customer: cust, Meter: "api", Period: "2026-W45", Windowed: true}, "lim:w:", true},
		{"year", Ref{Tenant: tn, Customer: cust, Meter: "api", Period: "2026", Windowed: true}, "lim:y:", true},
		{"lifetime", Ref{Tenant: tn, Customer: cust, Meter: "api", Period: period.LifetimeID, Windowed: true}, "lim:y:", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key := c.ref.Key()
			if len(key) < len(c.prefix) || key[:len(c.prefix)] != c.prefix {
				t.Fatalf("key %s, want prefix %s", key, c.prefix)
			}
			got, ok := ParseKey(key)
			if !ok || got != c.ref || got.Windowed != c.windowed {
				t.Fatalf("ParseKey(%s) = %+v %v, want %+v", key, got, ok, c.ref)
			}
		})
	}
}

func TestParseKeyRefusesAMismatchedPrefix(t *testing.T) {
	tn, cust := uuid.New(), uuid.New()
	for _, k := range []string{
		"lim:w:" + tn.String() + ":" + cust.String() + ":api:2026",
		"lim:w:" + tn.String() + ":" + cust.String() + ":api:all",
		"lim:y:" + tn.String() + ":" + cust.String() + ":api:2026-11-05",
		"lim:y:" + tn.String() + ":" + cust.String() + ":api:2026-11",
		"lim:x:" + tn.String() + ":" + cust.String() + ":api:2026",
		"lim:c:" + tn.String() + ":" + cust.String() + ":api",
		"lim:c:not-a-uuid:" + cust.String() + ":api:2026-11",
	} {
		if _, ok := ParseKey(k); ok {
			t.Errorf("ParseKey(%s) accepted", k)
		}
	}
}

func TestLongWindow(t *testing.T) {
	for id, want := range map[string]bool{"2026": true, "all": true, "2026-11": false, "2026-11-05": false, "2026-11-05T13": false, "2026-W45": false} {
		if LongWindow(id) != want {
			t.Errorf("LongWindow(%q) = %v", id, !want)
		}
	}
	// Whatever shape the period package gives an id, the key prefix is chosen by the right rule.
	at := time.Date(2026, 11, 5, 13, 30, 0, 0, time.UTC)
	for _, w := range period.Windows {
		id, _, _ := period.WindowOf(w, at, time.UTC)
		if got, want := LongWindow(id), w == period.Year || w == period.Lifetime; got != want {
			t.Errorf("LongWindow(%q) of the %s window = %v", id, w, got)
		}
	}
}
