package domain

import (
	"strings"
	"unicode/utf8"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// ValidateAddress checks an address as it is entered: each part at most 120 characters (the postal code 20), no control characters, a country that is an
// ISO 3166-1 alpha-2 code in capitals. Every part is optional. field names the request field for the violations.
func ValidateAddress(field string, a *plansv1.Address) error {
	var v Violations
	v.Address(field, a)
	return v.Err()
}

// Address records the violations of an address (see ValidateAddress).
func (v *Violations) Address(field string, a *plansv1.Address) {
	// A slice, not a map: the violations come in the order of the fields, so the message that names the first one is always the same.
	for _, part := range []struct {
		name, value string
		max         int
	}{
		{"line1", a.GetLine1(), 120}, {"line2", a.GetLine2(), 120}, {"city", a.GetCity(), 120}, {"region", a.GetRegion(), 120}, {"postal_code", a.GetPostalCode(), 20},
	} {
		switch {
		case utf8.RuneCountInString(part.value) > part.max:
			v.Add(field+"."+part.name, "too long")
		case strings.ContainsAny(part.value, "\n\r\t\x00"):
			v.Add(field+"."+part.name, "must be one line of text")
		}
	}
	if c := a.GetCountry(); c != "" && !countryRe.MatchString(c) {
		v.Add(field+".country", "must be an ISO 3166-1 alpha-2 code in capitals, e.g. TR")
	}
}
