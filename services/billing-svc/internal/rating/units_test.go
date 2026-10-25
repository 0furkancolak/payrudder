package rating

import "testing"

func TestStoredUnitsReadsDigitsOnly(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "1250": 1250, "-40": -40} {
		if got, err := StoredUnits(in); err != nil || got != want {
			t.Errorf("StoredUnits(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "1.250", "0x10", "1e3", "12 ", "abc"} {
		if _, err := StoredUnits(in); err == nil {
			t.Errorf("StoredUnits(%q) read something that is not stored units", in)
		}
	}
}
