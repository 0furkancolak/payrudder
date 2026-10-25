// Package quantity converts exact decimal quantities to and from scaled integer units (ADR 0010):
// units = quantity × 10^scale, with scale 0..6 fixed per meter. No floating point is involved anywhere.
package quantity

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/0furkancolak/payrudder/pkg/money"
)

// MaxScale is the largest meter precision.
const MaxScale = 6

var (
	// ErrSyntax: not a non-negative decimal like "12", "0.5", "1.250".
	ErrSyntax = errors.New("quantity: not a non-negative decimal")
	// ErrPrecision: more decimal places than the meter's scale allows (no silent rounding).
	ErrPrecision = errors.New("quantity: too many decimal places")
	// ErrRange: units don't fit in int64.
	ErrRange = errors.New("quantity: out of range")
)

var pow10 = [MaxScale + 1]int64{1, 10, 100, 1_000, 10_000, 100_000, 1_000_000}

// ParseUnits parses a non-negative decimal string into scaled units.
// Trailing zeros beyond scale are accepted ("1.500" at scale 1 → 15); other extra digits are ErrPrecision.
func ParseUnits(s string, scale int) (int64, error) {
	if scale < 0 || scale > MaxScale {
		return 0, fmt.Errorf("quantity: scale %d outside 0..%d", scale, MaxScale)
	}
	intPart, frac, hasDot := strings.Cut(s, ".")
	if intPart == "" || (hasDot && frac == "") || !digits(intPart) || !digits(frac) {
		return 0, ErrSyntax
	}
	frac = strings.TrimRight(frac, "0")
	if len(frac) > scale {
		return 0, ErrPrecision
	}
	intPart = strings.TrimLeft(intPart, "0")

	var units int64
	for _, c := range intPart {
		if units > (math.MaxInt64-int64(c-'0'))/10 {
			return 0, ErrRange
		}
		units = units*10 + int64(c-'0')
	}
	if units > math.MaxInt64/pow10[scale] {
		return 0, ErrRange
	}
	units *= pow10[scale]

	var f int64
	for i := 0; i < scale; i++ {
		f *= 10
		if i < len(frac) {
			f += int64(frac[i] - '0')
		}
	}
	if units > math.MaxInt64-f {
		return 0, ErrRange
	}
	return units + f, nil
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// FormatUnits renders units as a decimal with exactly scale fractional digits ("1250", 3 → "1.250").
func FormatUnits(units int64, scale int) string {
	if scale <= 0 || scale > MaxScale {
		return strconv.FormatInt(units, 10)
	}
	return money.FormatMinor(units, scale)
}
