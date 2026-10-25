package quantity_test

import (
	"errors"
	"testing"

	"pgregory.net/rapid"

	"github.com/0furkancolak/payrudder/pkg/quantity"
)

func TestParseUnits(t *testing.T) {
	tests := []struct {
		in    string
		scale int
		want  int64
		err   error
	}{
		{"0", 0, 0, nil},
		{"1500", 0, 1500, nil},
		{"1.25", 3, 1250, nil},
		{"1.250", 1, 0, quantity.ErrPrecision},
		{"1.500", 1, 15, nil},
		{"007.5", 1, 75, nil},
		{"0.000001", 6, 1, nil},
		{"0.0000001", 6, 0, quantity.ErrPrecision},
		{"9223372036854775807", 0, 9223372036854775807, nil},
		{"9223372036854775808", 0, 0, quantity.ErrRange},
		{"9223372036854.775807", 6, 9223372036854775807, nil},
		{"9223372036854.775808", 6, 0, quantity.ErrRange},
		{"-1", 0, 0, quantity.ErrSyntax},
		{"1e3", 0, 0, quantity.ErrSyntax},
		{"", 0, 0, quantity.ErrSyntax},
		{".5", 1, 0, quantity.ErrSyntax},
		{"5.", 1, 0, quantity.ErrSyntax},
		{"1,5", 1, 0, quantity.ErrSyntax},
		{" 1", 0, 0, quantity.ErrSyntax},
	}
	for _, tt := range tests {
		got, err := quantity.ParseUnits(tt.in, tt.scale)
		if !errors.Is(err, tt.err) || got != tt.want {
			t.Errorf("ParseUnits(%q, %d) = %d, %v; want %d, %v", tt.in, tt.scale, got, err, tt.want, tt.err)
		}
	}
	if _, err := quantity.ParseUnits("1", 7); err == nil {
		t.Error("scale 7 accepted")
	}
}

func TestFormatUnits(t *testing.T) {
	for _, tt := range []struct {
		units int64
		scale int
		want  string
	}{{1250, 3, "1.250"}, {5, 2, "0.05"}, {1500, 0, "1500"}, {-15, 1, "-1.5"}} {
		if got := quantity.FormatUnits(tt.units, tt.scale); got != tt.want {
			t.Errorf("FormatUnits(%d, %d) = %q, want %q", tt.units, tt.scale, got, tt.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		scale := rapid.IntRange(0, quantity.MaxScale).Draw(t, "scale")
		units := rapid.Int64Range(0, 1<<62).Draw(t, "units")
		s := quantity.FormatUnits(units, scale)
		got, err := quantity.ParseUnits(s, scale)
		if err != nil || got != units {
			t.Fatalf("round trip %d (scale %d) via %q = %d, %v", units, scale, s, got, err)
		}
	})
}
