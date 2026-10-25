package rating

import "strconv"

// StoredUnits reads the quantity of an invoice line the way it is stored: whole units of the meter's scale (1250 with scale 3 is 1.250), as digits.
func StoredUnits(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }
