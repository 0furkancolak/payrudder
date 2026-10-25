package idgen_test

import (
	"sort"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/idgen"
)

func TestUUIDv7Version(t *testing.T) {
	if v := idgen.NewUUIDv7().Version(); v != 7 {
		t.Fatalf("version = %d", v)
	}
}

func TestULIDMonotonicWithinMillisecond(t *testing.T) {
	now := time.UnixMilli(1_790_000_000_000)
	ids := make([]string, 1000)
	for i := range ids {
		ids[i] = idgen.NewULIDAt(now)
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatal("ULIDs in the same millisecond are not sorted")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate ULID %s", id)
		}
		seen[id] = true
	}
}

func TestDeterministic(t *testing.T) {
	a := idgen.Deterministic("saga-1", "charge", "0")
	if a != idgen.Deterministic("saga-1", "charge", "0") {
		t.Fatal("same input gave different IDs")
	}
	if a == idgen.Deterministic("saga-1", "charge", "1") {
		t.Fatal("different input gave the same ID")
	}
	if a.Version() != 5 {
		t.Fatalf("version = %d", a.Version())
	}
}
