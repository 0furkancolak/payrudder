package money

import (
	"math/big"
	"strconv"
	"strings"
)

// FormatMinor renders an integer amount with exp decimals using integer math only: (12345, 2) is "123.45", (-5, 2) is
// "-0.05", (7, 0) is "7". An exp <= 0 prints the integer.
func FormatMinor(v int64, exp int) string {
	neg := v < 0
	u := uint64(v) //nolint:gosec // negated below
	if neg {
		u = -u // exact for math.MinInt64 too
	}
	return place(strconv.FormatUint(u, 10), exp, neg)
}

// FormatMinorGrouped is FormatMinor with a comma between thousands of the whole part: (123456, 2) is "1,234.56".
func FormatMinorGrouped(v int64, exp int) string {
	neg := v < 0
	u := uint64(v) //nolint:gosec // negated below
	if neg {
		u = -u
	}
	s := place(strconv.FormatUint(u, 10), exp, false)
	whole, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i := 0; i < len(whole); i++ {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(whole[i])
	}
	if hasFrac {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	return b.String()
}

// FormatBig is FormatMinor for an arbitrary-precision integer.
func FormatBig(v *big.Int, exp int) string {
	s := v.String()
	neg := strings.HasPrefix(s, "-")
	return place(strings.TrimPrefix(s, "-"), exp, neg)
}

// FormatBps renders basis points as a percentage number without the sign: 2000 is "20.00", 750 is "7.50". With trimZeros
// the trailing zeros and a bare point go: 2000 is "20", 750 is "7.5", 825 is "8.25".
func FormatBps(bps int64, trimZeros bool) string {
	s := FormatMinor(bps, 2)
	if trimZeros {
		s = strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// place puts the decimal point exp digits from the right of digits (an unsigned decimal string), padding with zeros.
func place(digits string, exp int, neg bool) string {
	if exp > 0 {
		if len(digits) <= exp {
			digits = strings.Repeat("0", exp-len(digits)+1) + digits
		}
		digits = digits[:len(digits)-exp] + "." + digits[len(digits)-exp:]
	}
	if neg {
		return "-" + digits
	}
	return digits
}

// Abs is the absolute value of a minor-unit amount.
func Abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
