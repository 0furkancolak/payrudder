package query

import "strings"

// formatScaled renders an integer quantity in scaled units as a decimal with exactly scale fractional digits:
// ("1500", 3) -> "1.500", ("5", 3) -> "0.005". The input is the text of a numeric(38,0).
func formatScaled(digits string, scale int) string {
	neg := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	if scale > 0 {
		if len(digits) <= scale {
			digits = strings.Repeat("0", scale-len(digits)+1) + digits
		}
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if neg {
		return "-" + digits
	}
	return digits
}
