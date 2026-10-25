package money

import (
	"math"
	"math/big"
	"testing"
)

func TestFormatMinor(t *testing.T) {
	for _, c := range []struct {
		v    int64
		exp  int
		want string
	}{
		{12345, 2, "123.45"}, {-5, 2, "-0.05"}, {5, 2, "0.05"}, {0, 2, "0.00"}, {100, 2, "1.00"}, {7, 0, "7"}, {-7, 0, "-7"},
		{1116, 3, "1.116"}, {1, 3, "0.001"}, {42, -1, "42"}, {1250, 3, "1.250"},
		{math.MaxInt64, 2, "92233720368547758.07"}, {math.MinInt64, 2, "-92233720368547758.08"}, {math.MinInt64, 0, "-9223372036854775808"},
	} {
		if got := FormatMinor(c.v, c.exp); got != c.want {
			t.Errorf("FormatMinor(%d, %d) = %q, want %q", c.v, c.exp, got, c.want)
		}
	}
}

func TestFormatMinorGrouped(t *testing.T) {
	for _, c := range []struct {
		v    int64
		exp  int
		want string
	}{
		{123456, 2, "1,234.56"}, {99, 2, "0.99"}, {100000, 2, "1,000.00"}, {-123456789, 2, "-1,234,567.89"}, {1234, 0, "1,234"},
		{999, 0, "999"}, {-5, 2, "-0.05"}, {0, 0, "0"}, {math.MinInt64, 0, "-9,223,372,036,854,775,808"},
	} {
		if got := FormatMinorGrouped(c.v, c.exp); got != c.want {
			t.Errorf("FormatMinorGrouped(%d, %d) = %q, want %q", c.v, c.exp, got, c.want)
		}
	}
}

func TestFormatBig(t *testing.T) {
	v, _ := new(big.Int).SetString("-123456789012345678901234567890", 10)
	if got := FormatBig(v, 4); got != "-12345678901234567890123456.7890" {
		t.Errorf("FormatBig = %q", got)
	}
	if got := FormatBig(big.NewInt(5), 3); got != "0.005" {
		t.Errorf("FormatBig = %q", got)
	}
}

func TestFormatBps(t *testing.T) {
	for _, c := range []struct {
		bps  int64
		trim bool
		want string
	}{
		{2000, false, "20.00"}, {750, false, "7.50"}, {825, false, "8.25"}, {5, false, "0.05"}, {10000, false, "100.00"},
		{2000, true, "20"}, {750, true, "7.5"}, {825, true, "8.25"}, {5, true, "0.05"}, {0, true, "0"}, {10000, true, "100"}, {-250, true, "-2.5"},
	} {
		if got := FormatBps(c.bps, c.trim); got != c.want {
			t.Errorf("FormatBps(%d, %v) = %q, want %q", c.bps, c.trim, got, c.want)
		}
	}
}
